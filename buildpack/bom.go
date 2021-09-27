package buildpack

import (
	"github.com/buildpacks/lifecycle/api"
	"github.com/buildpacks/lifecycle/buildpack/dataformat"
	"github.com/buildpacks/lifecycle/buildpack/legacy"
	v05 "github.com/buildpacks/lifecycle/buildpack/v05"
	v07 "github.com/buildpacks/lifecycle/buildpack/v07"
)

func validateBOM(bom []dataformat.BOMEntry, bpAPI string) error {
	switch {
	case api.MustParse(bpAPI).LessThan("0.5"):
		bh := &legacy.BOMHandler{}
		return bh.HandleBOM(bom)
	case api.MustParse(bpAPI).LessThan("0.6"):
		bh := &v05.BOMHandler{}
		return bh.HandleBOM(bom)
	}
	bh := &v07.BOMHandler{} // TODO: add logger
	return bh.HandleBOM(bom)
}
