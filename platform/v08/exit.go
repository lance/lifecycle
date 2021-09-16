package v08

import (
	"github.com/buildpacks/lifecycle/cmd"
)

func (p *v08Platform) CodeFor(errType cmd.LifecycleExitError) int {
	return p.previousPlatform.CodeFor(errType)
}
