// Package golangci registers qanalysis's analyzers as a golangci-lint
// module plugin named "gotogether". In .custom-gcl.yml:
//
//	plugins:
//	  - module: github.com/chucklehead-dev/gotogether/qanalysis
//	    import: github.com/chucklehead-dev/gotogether/qanalysis/golangci
//	    version: <version>
//
// and in .golangci.yml:
//
//	linters:
//	  enable: [gotogether]
//	  settings:
//	    custom:
//	      gotogether:
//	        type: module
//	        settings:
//	          analyzers: [quintsim, quintaction]   # default: all three
//	          binding: app.binding.yaml            # quintaction's -binding
package golangci

import (
	"fmt"
	"slices"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"

	"github.com/chucklehead-dev/gotogether/qanalysis"
)

func init() { register.Plugin("gotogether", New) }

// Settings are the plugin's settings in .golangci.yml.
type Settings struct {
	// Analyzers to run, by name (quintsim, quintweave, quintaction; empty:
	// all).
	Analyzers []string `json:"analyzers"`
	// Binding and Quint are quintaction's -binding and -quint.
	Binding string `json:"binding"`
	Quint   string `json:"quint"`
}

// New is the plugin's constructor.
func New(conf any) (register.LinterPlugin, error) {
	s, err := register.DecodeSettings[Settings](conf)
	if err != nil {
		return nil, err
	}
	for name, v := range map[string]string{"binding": s.Binding, "quint": s.Quint} {
		if v != "" {
			if err := qanalysis.Action.Flags.Set(name, v); err != nil {
				return nil, err
			}
		}
	}
	return &plugin{s}, nil
}

type plugin struct{ s Settings }

// BuildAnalyzers implements register.LinterPlugin.
func (p *plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	if len(p.s.Analyzers) == 0 {
		return qanalysis.All, nil
	}
	var out []*analysis.Analyzer
	for _, name := range p.s.Analyzers {
		i := slices.IndexFunc(qanalysis.All, func(a *analysis.Analyzer) bool { return a.Name == name })
		if i < 0 {
			return nil, fmt.Errorf("gotogether: no analyzer %q (have quintsim, quintweave, quintaction)", name)
		}
		out = append(out, qanalysis.All[i])
	}
	return out, nil
}

// GetLoadMode implements register.LinterPlugin: quintaction needs types.
func (p *plugin) GetLoadMode() string { return register.LoadModeTypesInfo }
