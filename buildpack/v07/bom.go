package v07

import (
	"github.com/buildpacks/lifecycle/buildpack/common"
	"github.com/buildpacks/lifecycle/buildpack/dataformat"
)

type BOMHandler struct {
	Logger common.Logger
}

func (b *BOMHandler) HandleBOM(bom []dataformat.BOMEntry) error {
	if len(bom) > 0 {
		// TODO: warn
	}
	return nil
}
