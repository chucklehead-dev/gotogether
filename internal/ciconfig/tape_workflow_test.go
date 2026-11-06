package ciconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Tool acquisition must fail before missing tools can turn tape tests into
// skips. Keep the golden checks and actual media rendering mandatory.
func TestTapeBootstrapWorkflow(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Continue bool `yaml:"continue-on-error"`
			If       any  `yaml:"if"`
			Steps    []struct {
				Name     string `yaml:"name"`
				Run      string `yaml:"run"`
				Timeout  int    `yaml:"timeout-minutes"`
				Continue bool   `yaml:"continue-on-error"`
				If       any    `yaml:"if"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	job, ok := workflow.Jobs["qnav-tapes"]
	if !ok || job.Continue || job.If != nil {
		t.Fatal("qnav-tapes must be a mandatory job")
	}
	install, golden, render := false, false, false
	archiveIndex := -1
	installIndex, goldenIndex, renderIndex := -1, -1, -1
	for i, step := range job.Steps {
		switch run := strings.TrimSpace(step.Run); {
		case run == "../ci/install-tape-tools.sh":
			install = true
			installIndex = i
			if step.Timeout <= 0 || step.Timeout > 10 || step.Continue || step.If != nil {
				t.Error("tape bootstrap needs a finite step deadline and mandatory failure propagation")
			}
		case step.Name == "Select Ubuntu's primary archive for tape tools":
			archiveIndex = i
			if step.Timeout != 1 || step.Continue || step.If != nil {
				t.Error("archive selection must be bounded and mandatory")
			}
		case run == "go test -count=1 -run TestTapes -v ./tapes/":
			golden = true
			goldenIndex = i
			if step.Continue || step.If != nil {
				t.Error("tape golden checks must not be conditional or ignore failure")
			}
		case run == "tapes/render.sh":
			render = true
			renderIndex = i
			if step.Continue || step.If != nil {
				t.Error("tape rendering must not be conditional or ignore failure")
			}
		}
	}
	if !install || !golden || !render || installIndex >= goldenIndex || goldenIndex >= renderIndex {
		t.Fatal("qnav-tapes requires bounded tool bootstrap, golden checks and media rendering")
	}
	if archiveIndex < 0 || archiveIndex >= installIndex {
		t.Fatal("select the primary archive before acquiring tape tools")
	}
}

func TestTapeArchiveSelection(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ Name, Run string }
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["qnav-tapes"].Steps {
		if step.Name == "Select Ubuntu's primary archive for tape tools" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("archive selection is missing")
	}
	for _, mode := range []string{"select", "unreferenced", "write-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			sources := filepath.Join(dir, "ubuntu.sources")
			mirrors := filepath.Join(dir, "apt-mirrors.txt")
			uri := "mirror+file:" + mirrors
			if mode == "unreferenced" {
				uri = "http://azure.archive.ubuntu.com/ubuntu/\n# mirror+file:" + mirrors
			}
			before := fmt.Sprintf("Types: deb\nURIs: %s\nSuites: resolute resolute-updates\nComponents: main universe\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n\nTypes: deb\nURIs: https://security.ubuntu.com/ubuntu/\nSuites: resolute-security\nComponents: main universe\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n", uri)
			old := "http://azure.archive.ubuntu.com/ubuntu/\tpriority:1\nhttps://archive.ubuntu.com/ubuntu/\tpriority:2\n"
			for path, body := range map[string]string{sources: before, mirrors: old} {
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			sudo := "#!/bin/sh\nexec \"$@\"\n"
			if mode == "write-error" {
				sudo = "#!/bin/sh\nexit 17\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(sudo), 0700); err != nil {
				t.Fatal(err)
			}
			// Substitute fixture paths, then execute the workflow's actual shell.
			fixture := strings.NewReplacer("/etc/apt/sources.list.d/ubuntu.sources", sources, "/etc/apt/apt-mirrors.txt", mirrors).Replace(script)
			cmd := exec.Command(bash, "-e", "-u", "-o", "pipefail", "-c", fixture)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if (err == nil) != (mode == "select") {
				t.Fatalf("archive selection: error=%v output=%s", err, out)
			}
			after, err := os.ReadFile(sources)
			if err != nil || string(after) != before {
				t.Fatalf("repository suites, components, security or trust settings changed: %s (%v)", after, err)
			}
			got, err := os.ReadFile(mirrors)
			want := old
			if mode == "select" {
				want = "https://archive.ubuntu.com/ubuntu/\n"
			}
			if err != nil || string(got) != want {
				t.Fatalf("mirror selection = %q, want %q: %v", got, want, err)
			}
		})
	}
}
