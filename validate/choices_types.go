package validate

import "github.com/chucklehead-dev/gotogether/validate/receipt"

// DefaultChoiceLimit bounds expanded states of one observed actor's finite
// internal-choice search, including its admitted initial states.
const DefaultChoiceLimit = 20000

// ChoiceSearch discloses finite model-choice search and selected-path confirmation.
type ChoiceSearch = receipt.ChoiceSearch

// ChoiceInitialization is the original model initializer pinned to a legal state.
type ChoiceInitialization = receipt.ChoiceInitialization

// ChoiceStep is one original model action pinned to a finite legal choice.
type ChoiceStep = receipt.ChoiceStep
