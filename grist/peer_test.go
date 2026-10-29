package grist_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
)

type peerPay struct {
	Amount int `json:"amount"`
}

// A peer's declarations: NewPeerCall of a dependency that is no peer, of
// another App's command, or of an Internal one (its API serves it to no
// one) is a problem naming the fix, as is an App its own peer or an App's
// name holding a slash (an actor's separator). A peer call is Keyed, its
// refusals the command's, its response none; it is a peer's dependency
// in the catalog, which serves no Handlers: DefaultHandlers panics, and
// Main's -dependency refuses it.
func TestPeerDeclarations(t *testing.T) {
	bank := grist.New("bank")
	closed := grist.Rejection("closed")
	pay := grist.NewCommand(bank, "pay", func(r *grist.Reads, c peerPay) ([]grist.Event, error) { return nil, nil },
		grist.Unconditioned("reads nothing"), grist.Refuses(closed), grist.Authorize(grist.Callers(grist.PeerCaller("shop"))))
	secret := grist.NewCommand(bank, "secret", func(r *grist.Reads, c peerPay) ([]grist.Event, error) { return nil, nil },
		grist.Unconditioned("reads nothing"), grist.Internal())
	other := grist.New("other")
	foreign := grist.NewCommand(other, "pay", func(r *grist.Reads, c peerPay) ([]grist.Event, error) { return nil, nil },
		grist.Unconditioned("reads nothing"), grist.Authorize(grist.Authenticated))

	a := grist.New("shop")
	peer := grist.NewPeer(a, bank)
	call := grist.NewPeerCall(peer, pay)
	grist.NewPeerCall(peer, secret)
	grist.NewPeerCall(peer, foreign)
	grist.NewPeerCall(grist.NewDependency(a, "mailer"), pay)
	grist.NewPeer(a, a)
	err := a.Verify()
	for _, want := range []string{
		"NewPeerCall: command secret of bank is Internal, which its API serves to no one",
		"NewPeerCall: a command of another App than shop's peer bank",
		"NewPeerCall: dependency mailer is no peer: declare it with grist.NewPeer",
		"NewPeer: app shop declared its own peer",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Verify: %v\nwant it to say %s", err, want)
		}
	}
	if err := grist.New("a/b").Verify(); err == nil || !strings.Contains(err.Error(), `app "a/b": an App's name is non-empty and holds no slash`) {
		t.Errorf("an App named a/b: %v, want its slash refused", err)
	}

	cat := a.Catalog()
	if d := cat.Dependencies[0]; d.Name != "bank" || d.Peer != "bank" || len(d.Calls) < 1 || d.Calls[0].Quint != "bankPay" || !d.Calls[0].Keyed ||
		fmt.Sprint(d.Calls[0].Refuses) != "[closed]" || d.Calls[0].Response.NumField() != 0 || call.Name() != "pay" || peer.Peer() != bank {
		t.Errorf("the peer's catalog entry %+v: want bank's pay, Keyed, refusing closed, answering nothing", d)
	}
	func() {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "dependency bank is a peer (grist.NewPeer)") {
				t.Errorf("DefaultHandlers of a peer: %v, want a panic naming client.Peer", r)
			}
		}()
		grist.DefaultHandlers(peer)
	}()

	good := grist.New("shop")
	gpay := grist.NewPeerCall(grist.NewPeer(good, bank), pay)
	ev := grist.NewEvent[peerPay](good, "ordered")
	grist.NewCommand(good, "order", func(r *grist.Reads, c peerPay) ([]grist.Event, error) { return []grist.Event{ev.Of(c)}, nil },
		grist.Unconditioned("reads nothing"), grist.Authorize(grist.Authenticated))
	grist.NewProcessor(good, "charge", ev, func(ctx context.Context, r *grist.Reads, e peerPay, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error { _, err := gpay.Do(ctx, e); return err }, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(gpay))
	if err := good.Verify(); err != nil {
		t.Fatal(err)
	}
	var usage *grist.UsageError
	err = grist.MainArgs(context.Background(), good, []string{"serve", "-db", "sqlite:" + filepath.Join(t.TempDir(), "x.db"), "-dependency", "bank=http://bank:8080"}, io.Discard, io.Discard)
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "dependency bank is a peer") {
		t.Errorf("-dependency of a peer: %v, want a usage error naming client.Peer", err)
	}
}
