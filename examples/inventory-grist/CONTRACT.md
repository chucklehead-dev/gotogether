# Business contract for the inventory trial

One tenant has one SKU and two units. Two checkout clients buy one unit
each. Inventory's reusable reserve API allocates one unit per command,
including an explicit additional allocation to an existing hold. Checkout
must preserve the effect key when retrying its single allocation.

1. An order is placed once. Cancel and confirm accept customer intents,
   not promises that inventory has already acted.
2. A reservation names a placed order, preserves its identity, and
   allocates at most one unit for that checkout. Held or authorized
   stock never exceeds two units.
3. Inventory serializes cancellation and authorization. An accepted
   cancellation releases all held units and leaves a tombstone, including
   when cancellation overtakes reserve. Authorization after accepted
   cancellation is forbidden. Once authorized, cancel is too late.
4. Fulfillment starts at most once and only because inventory committed
   authorization for that order. A cancelled order never starts fulfillment.
5. At a recorded settle every accepted customer intent has an inventory
   answer, every accepted authorization has a fulfillment start, and all
   accepted cancellations have released their stock. A finite prefix has
   no completion verdict. Unconfirmed, uncancelled holds may remain.

Confirmation can be refused `not-held` if it overtakes reservation. This
trial does not retry that business refusal or claim every placed order
ships. It has no timers, external infrastructure or service cycles.
