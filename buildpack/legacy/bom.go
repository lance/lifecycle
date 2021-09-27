package legacy

import (
	"errors"
	"fmt"

	"github.com/buildpacks/lifecycle/buildpack/common"
	"github.com/buildpacks/lifecycle/buildpack/dataformat"
)

type BOMHandler struct {
	Logger common.Logger
}

func (b *BOMHandler) HandleBOM(bom []dataformat.BOMEntry) error {
	for _, entry := range bom {
		if version, ok := entry.Metadata["version"]; ok {
			metadataVersion := fmt.Sprintf("%v", version)
			if entry.Version != "" && entry.Version != metadataVersion {
				return errors.New("top level version does not match metadata version")
			}
		}
	}
	return nil
}
