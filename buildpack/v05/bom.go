package v05

import (
	"fmt"
	"github.com/buildpacks/lifecycle/buildpack/common"
	"github.com/buildpacks/lifecycle/buildpack/dataformat"
)

type BOMHandler struct {
	Logger common.Logger
}

func (b *BOMHandler) HandleBOM(bom []dataformat.BOMEntry) error {
	for _, entry := range bom {
		if entry.Version != "" {
			return fmt.Errorf("bom entry '%s' has a top level version which is not allowed. The buildpack should instead set metadata.version", entry.Name)
		}
	}
	return nil
}
