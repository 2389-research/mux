// tool/filter_test.go
package tool_test

import (
	"testing"

	"github.com/2389-research/mux/tool"
)

func TestNewFilteredRegistry(t *testing.T) {
	source := tool.NewRegistry()

	filtered := tool.NewFilteredRegistry(source, nil, nil)

	if filtered == nil {
		t.Fatal("expected non-nil FilteredRegistry")
	}
}
