package rec

import "strings"

// Actor is the actor of tenant's store-side steps in app: <app>/<tenant>
// (docs/grist-instrumentation.md §5, decision 1), so two Apps' tenant t1
// in one run are two instances. Neither name holds a slash (grist.New,
// grist.CheckTenant).
func Actor(app, tenant string) string { return app + "/" + tenant }

// ClientsSuffix ends a client-side actor.
const ClientsSuffix = "/clients"

// ClientsActor is the actor of tenant's client-side steps in app:
// <app>/<tenant>/clients.
func ClientsActor(app, tenant string) string { return Actor(app, tenant) + ClientsSuffix }

// ParseActor splits a grist actor into its App, its tenant and whether it
// is the clients' side; ok is false for an actor no grist step has (no
// App, or a slash too many).
func ParseActor(actor string) (app, tenant string, clients, ok bool) {
	parts := strings.Split(actor, "/")
	switch {
	case len(parts) == 2:
	case len(parts) == 3 && "/"+parts[2] == ClientsSuffix:
		clients = true
	default:
		return "", "", false, false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false, false
	}
	return parts[0], parts[1], clients, true
}
