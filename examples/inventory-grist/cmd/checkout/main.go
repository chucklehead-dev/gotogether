package main

import (
	"context"

	"github.com/chucklehead-dev/gotogether/examples/inventory-grist"
	"github.com/chucklehead-dev/gotogether/examples/inventory-grist/inventory"
	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/client"
	"os"
)

func main() {
	transport := client.Peer(&client.Client{App: inventory.App, URL: os.Getenv("INVENTORY_URL")})
	grist.Main(checkout.App, grist.Dependencies(func(ctx context.Context) context.Context { return checkout.Inventory.With(ctx, transport) }))
}
