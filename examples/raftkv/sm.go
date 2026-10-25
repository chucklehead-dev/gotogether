package raftkv

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Op is a client's request, as the log carries it: the operation, its
// keys and numbers, and the request id (the client and its sequence
// number) that makes a retry apply once.
type Op struct {
	Kind   string // put, get, cas, transfer; version, the cluster's
	Key    string // put, get, cas; transfer's source
	Dest   string // transfer's
	A, B   int    // put's value; cas's expected and desired; transfer's amount
	Client int
	Seq    int
}

// Result is what an applied operation answered: for a put, a cas or a
// transfer whether it took effect; for a get the value.
type Result struct {
	OK  bool
	Val int
}

// session is a client's last applied request and its result, so a retry
// of it answers the same without applying again.
type session struct {
	Seq    int
	Result Result
}

// SM is the replicated state machine: the keys' values, each client's
// session, and the log format the cluster writes (1 until every node
// runs v2 and the cluster committed the switch).
type SM struct {
	KV       map[string]int
	Sessions map[int]session
	Version  int
}

func newSM(keys []string, initial int) *SM {
	sm := &SM{KV: map[string]int{}, Sessions: map[int]session{}, Version: 1}
	for _, k := range keys {
		sm.KV[k] = initial
	}
	return sm
}

// apply applies op once per request id, and returns its result: a
// request already applied answers as it did.
func (sm *SM) apply(op Op) Result {
	if op.Kind == "version" {
		sm.Version = max(sm.Version, op.A)
		return Result{OK: true}
	}
	if s, ok := sm.Sessions[op.Client]; ok && s.Seq >= op.Seq {
		return s.Result
	}
	var r Result
	switch op.Kind {
	case "put":
		sm.KV[op.Key], r.OK = op.A, true
	case "cas":
		if sm.KV[op.Key] == op.A {
			sm.KV[op.Key], r.OK = op.B, true
		}
	case "transfer":
		if op.Key != op.Dest && sm.KV[op.Key] >= op.A {
			sm.KV[op.Key] -= op.A
			sm.KV[op.Dest] += op.A
			r.OK = true
		}
	}
	if op.Seq > sm.Sessions[op.Client].Seq {
		sm.Sessions[op.Client] = session{Seq: op.Seq, Result: r}
	}
	return r
}

// encode is the snapshot's data: JSON, whose maps are written in key
// order, so two replicas of one state write the same bytes.
func (sm *SM) encode() []byte {
	b, err := json.Marshal(sm)
	if err != nil {
		panic(err)
	}
	return b
}

func decodeSM(b []byte) *SM {
	sm := &SM{}
	if err := json.Unmarshal(b, sm); err != nil {
		panic(fmt.Sprintf("raftkv: a snapshot that does not decode: %v", err))
	}
	if sm.Sessions == nil {
		sm.Sessions = map[int]session{}
	}
	return sm
}

// The log's two formats. v1 is text, fields split by "|"; v2 is binary,
// its first byte 2. A v1 node cannot read v2 entries: it skips them, as
// old code skips a record it does not know, which is why the cluster
// writes v1 until every node can read v2.

func encodeV1(op Op) []byte {
	return []byte(strings.Join([]string{op.Kind, op.Key, op.Dest, strconv.Itoa(op.A), strconv.Itoa(op.B),
		strconv.Itoa(op.Client), strconv.Itoa(op.Seq)}, "|"))
}

func encodeV2(op Op) []byte {
	b := []byte{2}
	for _, s := range []string{op.Kind, op.Key, op.Dest} {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}
	for _, n := range []int{op.A, op.B, op.Client, op.Seq} {
		b = binary.AppendVarint(b, int64(n))
	}
	return b
}

var errFormat = errors.New("raftkv: an entry of a format this version cannot read")

// decode reads an entry of either format; v1 code (v2 false) reads only
// its own.
func decode(b []byte, v2 bool) (Op, error) {
	if len(b) > 0 && b[0] == 2 {
		if !v2 {
			return Op{}, errFormat
		}
		var op Op
		r := b[1:]
		var strs [3]string
		for i := range strs {
			n, k := binary.Uvarint(r)
			if k <= 0 || int(n) > len(r)-k {
				return Op{}, errFormat
			}
			strs[i], r = string(r[k:k+int(n)]), r[k+int(n):]
		}
		var nums [4]int
		for i := range nums {
			n, k := binary.Varint(r)
			if k <= 0 {
				return Op{}, errFormat
			}
			nums[i], r = int(n), r[k:]
		}
		op.Kind, op.Key, op.Dest = strs[0], strs[1], strs[2]
		op.A, op.B, op.Client, op.Seq = nums[0], nums[1], nums[2], nums[3]
		return op, nil
	}
	f := strings.Split(string(b), "|")
	if len(f) != 7 {
		return Op{}, errFormat
	}
	var nums [4]int
	for i := range nums {
		n, err := strconv.Atoi(f[3+i])
		if err != nil {
			return Op{}, errFormat
		}
		nums[i] = n
	}
	return Op{Kind: f[0], Key: f[1], Dest: f[2], A: nums[0], B: nums[1], Client: nums[2], Seq: nums[3]}, nil
}
