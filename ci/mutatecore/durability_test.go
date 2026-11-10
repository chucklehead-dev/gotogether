package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type cancelDuringReceiptEncoding struct {
	cancel context.CancelCauseFunc
	cause  error
}

func (v cancelDuringReceiptEncoding) MarshalJSON() ([]byte, error) {
	v.cancel(v.cause)
	return []byte(`{"complete":true}`), nil
}

func TestCanceledFinalizationDoesNotPublishReceipt(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "already canceled", true: "canceled during final encoding"}[late], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "receipt.json")
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			cause := errors.New("canceled after worker completed and evidence was checked")
			var value any = map[string]bool{"complete": true}
			if late {
				value = cancelDuringReceiptEncoding{cancel: cancel, cause: cause}
			} else {
				cancel(cause)
			}
			if err := publishJSONContext(ctx, path, value); !errors.Is(err, cause) {
				t.Fatalf("final cancellation lost: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("canceled finalization published receipt: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("canceled publication left evidence name/temp: %v", entries)
			}
		})
	}
}

func TestDirectoryAncestryIncludesCampaignNameAndAllParents(t *testing.T) {
	for _, name := range []string{"default campaign", "explicit journal"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".gotogether", "mutations")
			if name == "explicit journal" {
				dir = filepath.Join(root, "new", "nested", "explicit")
			}
			if err := durableMkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if name == "default campaign" {
				var err error
				dir, err = os.MkdirTemp(dir, "qenv-")
				if err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			if err := syncDirectoryAncestry(dir, func(path string) error {
				got = append(got, path)
				return syncDirectory(path)
			}); err != nil {
				t.Fatal(err)
			}
			var want []string
			for path := dir; ; path = filepath.Dir(path) {
				want = append(want, path)
				if filepath.Dir(path) == path {
					break
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("durability missed directory entry ancestry: got %v want %v", got, want)
			}
			// A failure persisting the name of .gotogether/new in its parent must
			// fail the operation, even after the deeper directories synced normally.
			sentinel := errors.New("parent directory Sync failed")
			err := syncDirectoryAncestry(dir, func(path string) error {
				if path == root {
					return sentinel
				}
				return syncDirectory(path)
			})
			if !errors.Is(err, sentinel) {
				t.Fatalf("ancestry Sync failure accepted: %v", err)
			}
		})
	}
}
