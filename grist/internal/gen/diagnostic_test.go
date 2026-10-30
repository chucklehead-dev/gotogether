package gen

import "testing"

func TestDiagnosticDocCoexistsWithGristMarks(t *testing.T) {
	src := []byte(`module shop {
  // grist: eventually
  /// gotogether: diagnostic outstanding
  val complete = outstanding.size() == 0
}`)
	marks, err := parseMarks(src, "shop")
	if err != nil {
		t.Fatal(err)
	}
	values := marks["eventually"]["default"]
	if len(values) != 1 || values[0] != "complete" {
		t.Fatalf("diagnostic changed generated check selection: %v", marks)
	}
}
