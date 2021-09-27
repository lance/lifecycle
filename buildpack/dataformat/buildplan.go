package dataformat

type BuildPlan struct {
	PlanSections
	Or PlanSectionsList `toml:"or"`
}

func (p *PlanSections) HasInconsistentVersions() bool {
	for _, req := range p.Requires {
		if req.HasInconsistentVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSections) HasDoublySpecifiedVersions() bool {
	for _, req := range p.Requires {
		if req.HasDoublySpecifiedVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSections) HasTopLevelVersions() bool {
	for _, req := range p.Requires {
		if req.HasTopLevelVersions() {
			return true
		}
	}
	return false
}

type PlanSectionsList []PlanSections

func (p *PlanSectionsList) HasInconsistentVersions() bool {
	for _, planSection := range *p {
		if planSection.HasInconsistentVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSectionsList) HasDoublySpecifiedVersions() bool {
	for _, planSection := range *p {
		if planSection.HasDoublySpecifiedVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSectionsList) HasTopLevelVersions() bool {
	for _, planSection := range *p {
		if planSection.HasTopLevelVersions() {
			return true
		}
	}
	return false
}

type PlanSections struct {
	Requires []Require `toml:"requires"`
	Provides []Provide `toml:"provides"`
}

type Provide struct {
	Name string `toml:"name"`
}
