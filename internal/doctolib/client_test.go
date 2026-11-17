package doctolib_test

import (
	"testing"

	"github.com/tio-plato/doctolib-mcp/internal/doctolib"
)

func TestChromeMajorComesFromTheEnvironment(t *testing.T) {
	t.Setenv(doctolib.ChromeMajorEnv, "999")
	major, err := doctolib.ChromeMajor()
	if err != nil || major != "999" {
		t.Fatalf("ChromeMajor() = %q, %v; want 999", major, err)
	}
	t.Setenv(doctolib.ChromeMajorEnv, "latest")
	_, err = doctolib.ChromeMajor()
	if err == nil {
		t.Error("accepted a Chrome version that is not a number")
	}
}
