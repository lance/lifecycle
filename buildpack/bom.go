package buildpack

import (
	"errors"
	"fmt"

	"github.com/buildpacks/lifecycle/api"
)

type BOMValidator interface {
	ValidateBOM(GroupBuildable, []BOMEntry) ([]BOMEntry, error)
}

func NewBOMValidator(bpAPI string, logger Logger) BOMValidator {
	switch {
	case api.MustParse(bpAPI).LessThan("0.5"):
		return &legacyBOMValidator{}
	case api.MustParse(bpAPI).LessThan("0.7"):
		return &v05To06BOMValidator{}
	default:
		return &defaultBOMValidator{logger: logger}
	}
}

type defaultBOMValidator struct {
	logger Logger
}

func (v *defaultBOMValidator) ValidateBOM(bp GroupBuildable, bom []BOMEntry) ([]BOMEntry, error) {
	if err := v.validateBOM(bom); err != nil {
		return []BOMEntry{}, err
	}
	return v.processBOM(bp, bom), nil
}

func (v *defaultBOMValidator) validateBOM(bom []BOMEntry) error {
	return nil
}

func (v *defaultBOMValidator) processBOM(_ GroupBuildable, _ []BOMEntry) []BOMEntry {
	return []BOMEntry{}
}

type v05To06BOMValidator struct{}

func (v *v05To06BOMValidator) ValidateBOM(bp GroupBuildable, bom []BOMEntry) ([]BOMEntry, error) {
	if err := v.validateBOM(bom); err != nil {
		return []BOMEntry{}, err
	}
	return v.processBOM(bp, bom), nil
}

func (v *v05To06BOMValidator) validateBOM(bom []BOMEntry) error {
	for _, entry := range bom {
		if entry.Version != "" {
			return fmt.Errorf("bom entry '%s' has a top level version which is not allowed. The buildpack should instead set metadata.version", entry.Name)
		}
	}
	return nil
}

func (v *v05To06BOMValidator) processBOM(buildpack GroupBuildable, bom []BOMEntry) []BOMEntry {
	return WithBuildpack(buildpack, bom)
}

type legacyBOMValidator struct{}

func (v *legacyBOMValidator) ValidateBOM(bp GroupBuildable, bom []BOMEntry) ([]BOMEntry, error) {
	if err := v.validateBOM(bom); err != nil {
		return []BOMEntry{}, err
	}
	return v.processBOM(bp, bom), nil
}

func (v *legacyBOMValidator) validateBOM(bom []BOMEntry) error {
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

func (v *legacyBOMValidator) processBOM(buildpack GroupBuildable, bom []BOMEntry) []BOMEntry {
	bom = WithBuildpack(buildpack, bom)
	for i := range bom {
		bom[i].convertVersionToMetadata()
	}
	return bom
}
