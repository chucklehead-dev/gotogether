package main

import (
	"context"

	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/fulfillment"
	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/inventory"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"os"
)

func main() {
	transport := client.Peer(&client.Client{App: fulfillment.App, URL: os.Getenv("FULFILLMENT_URL")})
	grist.Main(inventory.App, grist.Dependencies(func(ctx context.Context) context.Context { return inventory.Fulfillment.With(ctx, transport) }))
}
