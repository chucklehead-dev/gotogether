package kinesislog_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qenv/simlog/kinesislog"
	"github.com/chucklehead-dev/gotogether/qenv/simlog/kinesislog/kinesisfake"
)

// The SDK talks to the native fake through a one-shot second-shard fault.
// Its earlier shard cursor has already advanced, so those records must remain
// visible and committable alongside the SDK error, then survive reopen.
func TestPollReturnsRecordsBeforeLaterShardError(t *testing.T) {
	for _, operation := range []string{"GetShardIterator", "GetRecords"} {
		t.Run(operation, func(t *testing.T) {
			base := kinesisfake.New("events", 2)
			t.Cleanup(base.Close)
			var failed atomic.Bool
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(body, &fields); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				str := func(key string) string { var v string; _ = json.Unmarshal(fields[key], &v); return v }
				second := str("ShardId") == "shardId-000000000001"
				if iter, err := base64.StdEncoding.DecodeString(str("ShardIterator")); err == nil && strings.HasPrefix(string(iter), "1:") {
					second = true
				}
				if r.Header.Get("X-Amz-Target") == "Kinesis_20131202."+operation && second && failed.CompareAndSwap(false, true) {
					w.Header().Set("Content-Type", "application/x-amz-json-1.1")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"__type":"ResourceNotFoundException","message":"controlled second-shard failure"}`)
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(endpoint.Close)
			client := kinesislog.Client(endpoint.URL)
			writer := &kinesislog.Writer{C: client, Stream: "events"}
			// Native Kinesis hashing sends a to shard 0 and b to shard 1.
			produce(t, writer, []string{"a", "b"}, "1")
			checkpoints := &kinesislog.Checkpoints{}
			reader := &kinesislog.Reader{C: client, Stream: "events", Checkpoints: checkpoints}
			first, err := reader.Poll(t.Context())
			if typed, ok := errors.AsType[*types.ResourceNotFoundException](err); !ok || typed.ErrorMessage() != "controlled second-shard failure" {
				t.Fatalf("lost SDK error type/cause: %v", err)
			}
			if len(first) != 1 || first[0].Partition != 0 || first[0].Offset != 0 || first[0].Key != "a" || string(first[0].Value) != "a1" {
				t.Fatalf("earlier shard records lost: %+v", first)
			}
			if err := reader.Commit(t.Context(), first[0].Partition, first[0].Offset+1); err != nil {
				t.Fatalf("partial batch cannot be checkpointed: %v", err)
			}
			second, err := reader.Poll(t.Context())
			if err != nil || len(second) != 1 || second[0].Partition != 1 || second[0].Offset != 0 || second[0].Key != "b" || string(second[0].Value) != "b1" {
				t.Fatalf("retry duplicated or skipped data: %+v, %v", second, err)
			}
			if err := reader.Commit(t.Context(), second[0].Partition, second[0].Offset+1); err != nil {
				t.Fatal(err)
			}
			produce(t, writer, []string{"a", "b"}, "2")
			reopened := &kinesislog.Reader{C: client, Stream: "events", Checkpoints: checkpoints}
			next, err := reopened.Poll(t.Context())
			if err != nil || len(next) != 2 || next[0].Partition != 0 || next[1].Partition != 1 || next[0].Offset != 1 || next[1].Offset != 1 || string(next[0].Value) != "a2" || string(next[1].Value) != "b2" {
				t.Fatalf("checkpoint reopen: %+v, %v", next, err)
			}
		})
	}
}

type memoryKinesisHTTP func(*http.Request) (*http.Response, error)

func (f memoryKinesisHTTP) Do(r *http.Request) (*http.Response, error) { return f(r) }

// This SDK transport performs no I/O, so synctest controls the idle timer and
// parent deadline after all shard requests have completed.
func TestIdlePollReturnsParentContextError(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := kinesis.New(kinesis.Options{Region: "us-east-1", BaseEndpoint: aws.String("http://kinesis.invalid"), Credentials: aws.AnonymousCredentials{}, HTTPClient: memoryKinesisHTTP(func(req *http.Request) (*http.Response, error) {
					var payload string
					switch req.Header.Get("X-Amz-Target") {
					case "Kinesis_20131202.ListShards":
						payload = `{"Shards":[{"ShardId":"shard-0"}]}`
					case "Kinesis_20131202.GetShardIterator":
						payload = `{"ShardIterator":"cursor"}`
					case "Kinesis_20131202.GetRecords":
						payload = `{"Records":[],"NextShardIterator":"cursor"}`
					default:
						t.Errorf("unexpected SDK request %q", req.Header.Get("X-Amz-Target"))
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/x-amz-json-1.1"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})})
				var ctx context.Context
				var cancel context.CancelFunc
				want := error(context.Canceled)
				if deadline {
					ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
					want = context.DeadlineExceeded
				} else {
					ctx, cancel = context.WithCancel(t.Context())
				}
				defer cancel()
				reader := &kinesislog.Reader{C: client, Stream: "events", Checkpoints: &kinesislog.Checkpoints{}}
				type outcome struct {
					records []simlog.Record
					err     error
				}
				done := make(chan outcome, 1)
				go func() { records, err := reader.Poll(ctx); done <- outcome{records, err} }()
				synctest.Wait()
				if !deadline {
					cancel()
				}
				got := <-done
				if len(got.records) != 0 || !errors.Is(got.err, want) {
					t.Fatalf("idle poll returned %+v, %v; want %v", got.records, got.err, want)
				}
			})
		})
	}
}
