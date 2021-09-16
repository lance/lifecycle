package v08

import (
	"github.com/buildpacks/lifecycle/api"
	"github.com/buildpacks/lifecycle/platform/common"
)

type v08Platform struct {
	api              *api.Version
	previousPlatform common.Platform
}

func NewPlatform(previousPlatform common.Platform) common.Platform {
	return &v08Platform{
		api:              api.MustParse("0.8"),
		previousPlatform: previousPlatform,
	}
}

func (p *v08Platform) API() string {
	return p.api.String()
}
