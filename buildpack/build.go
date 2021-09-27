package buildpack

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/buildpacks/lifecycle/buildpack/common"
	"github.com/buildpacks/lifecycle/buildpack/dataformat"

	"github.com/BurntSushi/toml"

	"github.com/buildpacks/lifecycle/api"
	"github.com/buildpacks/lifecycle/env"
	"github.com/buildpacks/lifecycle/launch"
	"github.com/buildpacks/lifecycle/layers"
)

type BuildEnv interface {
	AddRootDir(baseDir string) error
	AddEnvDir(envDir string, defaultAction env.ActionType) error
	WithPlatform(platformDir string) ([]string, error)
	List() []string
}

type BuildConfig struct {
	AppDir      string
	PlatformDir string
	LayersDir   string
	Out         io.Writer
	Err         io.Writer
	Logger      common.Logger
}

type BuildResult struct {
	BOM         []dataformat.BOMEntry
	Labels      []dataformat.Label
	MetRequires []string
	Processes   []launch.Process
	Slices      []layers.Slice
}

func (b *Descriptor) Build(bpPlan dataformat.Plan, config BuildConfig, bpEnv BuildEnv) (BuildResult, error) {
	config.Logger.Debugf("Running build for buildpack %s", b)

	if api.MustParse(b.API).Equal(api.MustParse("0.2")) {
		config.Logger.Debug("Updating buildpack plan entries")

		for i := range bpPlan.Entries {
			bpPlan.Entries[i].ConvertMetadataToVersion()
		}
	}

	config.Logger.Debug("Creating plan directory")
	planDir, err := ioutil.TempDir("", launch.EscapeID(b.Buildpack.ID)+"-")
	if err != nil {
		return BuildResult{}, err
	}
	defer os.RemoveAll(planDir)

	config.Logger.Debug("Preparing paths")
	bpLayersDir, bpPlanPath, err := preparePaths(b.Buildpack.ID, bpPlan, config.LayersDir, planDir)
	if err != nil {
		return BuildResult{}, err
	}

	config.Logger.Debug("Running build command")
	if err := b.runBuildCmd(bpLayersDir, bpPlanPath, config, bpEnv); err != nil {
		return BuildResult{}, err
	}

	config.Logger.Debug("Processing layers")
	pathToLayerMetadataFile, err := b.processLayers(bpLayersDir, config.Logger)
	if err != nil {
		return BuildResult{}, err
	}

	config.Logger.Debug("Updating environment")
	if err := b.setupEnv(pathToLayerMetadataFile, bpEnv); err != nil {
		return BuildResult{}, err
	}

	config.Logger.Debug("Reading output files")
	return b.readOutputFiles(bpLayersDir, bpPlanPath, bpPlan, config.Logger)
}

func renameLayerDirIfNeeded(layerMetadataFile dataformat.LayerMetadataFile, layerDir string) error {
	// rename <layers>/<layer> to <layers>/<layer>.ignore if buildpack API >= 0.6 and all of the types flags are set to false
	if !layerMetadataFile.Launch && !layerMetadataFile.Cache && !layerMetadataFile.Build {
		if err := os.Rename(layerDir, layerDir+".ignore"); err != nil {
			return err
		}
	}
	return nil
}

func (b *Descriptor) processLayers(layersDir string, logger common.Logger) (map[string]dataformat.LayerMetadataFile, error) {
	if api.MustParse(b.API).LessThan("0.6") {
		return eachDir(layersDir, b.API, func(path, buildpackAPI string) (dataformat.LayerMetadataFile, error) {
			layerMetadataFile, msg, err := DecodeLayerMetadataFile(path+".toml", buildpackAPI)
			if err != nil {
				return dataformat.LayerMetadataFile{}, err
			}
			if msg != "" {
				logger.Warn(msg)
			}
			return layerMetadataFile, nil
		})
	}
	return eachDir(layersDir, b.API, func(path, buildpackAPI string) (dataformat.LayerMetadataFile, error) {
		layerMetadataFile, msg, err := DecodeLayerMetadataFile(path+".toml", buildpackAPI)
		if err != nil {
			return dataformat.LayerMetadataFile{}, err
		}
		if msg != "" {
			return dataformat.LayerMetadataFile{}, errors.New(msg)
		}
		if err := renameLayerDirIfNeeded(layerMetadataFile, path); err != nil {
			return dataformat.LayerMetadataFile{}, err
		}
		return layerMetadataFile, nil
	})
}

func preparePaths(bpID string, bpPlan dataformat.Plan, layersDir, planDir string) (string, string, error) {
	bpDirName := launch.EscapeID(bpID)
	bpLayersDir := filepath.Join(layersDir, bpDirName)
	bpPlanDir := filepath.Join(planDir, bpDirName)
	if err := os.MkdirAll(bpLayersDir, 0777); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(bpPlanDir, 0777); err != nil {
		return "", "", err
	}
	bpPlanPath := filepath.Join(bpPlanDir, "plan.toml")
	if err := WriteTOML(bpPlanPath, bpPlan); err != nil {
		return "", "", err
	}

	return bpLayersDir, bpPlanPath, nil
}

func WriteTOML(path string, data interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(data)
}

func (b *Descriptor) runBuildCmd(bpLayersDir, bpPlanPath string, config BuildConfig, bpEnv BuildEnv) error {
	cmd := exec.Command(
		filepath.Join(b.Dir, "bin", "build"),
		bpLayersDir,
		config.PlatformDir,
		bpPlanPath,
	) // #nosec G204
	cmd.Dir = config.AppDir
	cmd.Stdout = config.Out
	cmd.Stderr = config.Err

	var err error
	if b.Buildpack.ClearEnv {
		cmd.Env = bpEnv.List()
	} else {
		cmd.Env, err = bpEnv.WithPlatform(config.PlatformDir)
		if err != nil {
			return err
		}
	}
	cmd.Env = append(cmd.Env, EnvBuildpackDir+"="+b.Dir)

	if err := cmd.Run(); err != nil {
		return NewLifecycleError(err, ErrTypeBuildpack)
	}
	return nil
}

func (b *Descriptor) setupEnv(pathToLayerMetadataFile map[string]dataformat.LayerMetadataFile, buildEnv BuildEnv) error {
	bpAPI := api.MustParse(b.API)
	for path, layerMetadataFile := range pathToLayerMetadataFile {
		if !layerMetadataFile.Build {
			continue
		}
		if err := buildEnv.AddRootDir(path); err != nil {
			return err
		}
		if err := buildEnv.AddEnvDir(filepath.Join(path, "env"), env.DefaultActionType(bpAPI)); err != nil {
			return err
		}
		if err := buildEnv.AddEnvDir(filepath.Join(path, "env.build"), env.DefaultActionType(bpAPI)); err != nil {
			return err
		}
	}
	return nil
}

func eachDir(dir, buildpackAPI string, fn func(path, api string) (dataformat.LayerMetadataFile, error)) (map[string]dataformat.LayerMetadataFile, error) {
	files, err := ioutil.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]dataformat.LayerMetadataFile{}, nil
	} else if err != nil {
		return map[string]dataformat.LayerMetadataFile{}, err
	}
	pathToLayerMetadataFile := map[string]dataformat.LayerMetadataFile{}
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		path := filepath.Join(dir, f.Name())
		layerMetadataFile, err := fn(path, buildpackAPI)
		if err != nil {
			return map[string]dataformat.LayerMetadataFile{}, err
		}
		pathToLayerMetadataFile[path] = layerMetadataFile
	}
	return pathToLayerMetadataFile, nil
}

func (b *Descriptor) readOutputFiles(bpLayersDir, bpPlanPath string, bpPlanIn dataformat.Plan, logger common.Logger) (BuildResult, error) {
	br := BuildResult{}
	bpFromBpInfo := GroupBuildpack{ID: b.Buildpack.ID, Version: b.Buildpack.Version}

	// setup launch.toml
	var launchTOML dataformat.LaunchTOML
	launchPath := filepath.Join(bpLayersDir, "launch.toml")

	if api.MustParse(b.API).LessThan("0.5") {
		// read buildpack plan
		var bpPlanOut dataformat.Plan
		if _, err := toml.DecodeFile(bpPlanPath, &bpPlanOut); err != nil {
			return BuildResult{}, err
		}

		// set BOM and MetRequires
		if err := validateBOM(bpPlanOut.ToBOM(), b.API); err != nil {
			return BuildResult{}, err
		}
		br.BOM = WithBuildpack(bpFromBpInfo, bpPlanOut.ToBOM())
		for i := range br.BOM {
			br.BOM[i].ConvertVersionToMetadata()
		}
		br.MetRequires = names(bpPlanOut.Entries)

		// read launch.toml, return if not exists
		if _, err := toml.DecodeFile(launchPath, &launchTOML); os.IsNotExist(err) {
			return br, nil
		} else if err != nil {
			return BuildResult{}, err
		}
	} else {
		// read build.toml
		var bpBuild dataformat.BuildTOML
		buildPath := filepath.Join(bpLayersDir, "build.toml")
		if _, err := toml.DecodeFile(buildPath, &bpBuild); err != nil && !os.IsNotExist(err) {
			return BuildResult{}, err
		}
		if err := validateBOM(bpBuild.BOM, b.API); err != nil {
			return BuildResult{}, err
		}

		// set MetRequires
		if err := validateUnmet(bpBuild.Unmet, bpPlanIn); err != nil {
			return BuildResult{}, err
		}
		br.MetRequires = names(bpPlanIn.Filter(bpBuild.Unmet).Entries)

		// read launch.toml, return if not exists
		if _, err := toml.DecodeFile(launchPath, &launchTOML); os.IsNotExist(err) {
			return br, nil
		} else if err != nil {
			return BuildResult{}, err
		}

		// set BOM
		if err := validateBOM(launchTOML.BOM, b.API); err != nil {
			return BuildResult{}, err
		}
		br.BOM = WithBuildpack(bpFromBpInfo, launchTOML.BOM)
	}

	if err := overrideDefaultForOldBuildpacks(launchTOML.Processes, b.API, logger); err != nil {
		return BuildResult{}, err
	}

	if err := validateNoMultipleDefaults(launchTOML.Processes); err != nil {
		return BuildResult{}, err
	}

	// set data from launch.toml
	br.Labels = append([]dataformat.Label{}, launchTOML.Labels...)
	for i := range launchTOML.Processes {
		launchTOML.Processes[i].BuildpackID = b.Buildpack.ID
	}
	br.Processes = append([]launch.Process{}, launchTOML.Processes...)
	br.Slices = append([]layers.Slice{}, launchTOML.Slices...)

	return br, nil
}

func overrideDefaultForOldBuildpacks(processes []launch.Process, bpAPI string, logger common.Logger) error {
	if api.MustParse(bpAPI).AtLeast("0.6") {
		return nil
	}
	replacedDefaults := []string{}
	for i := range processes {
		if processes[i].Default {
			replacedDefaults = append(replacedDefaults, processes[i].Type)
		}
		processes[i].Default = false
	}
	if len(replacedDefaults) > 0 {
		logger.Warn(fmt.Sprintf("Warning: default processes aren't supported in this buildpack api version. Overriding the default value to false for the following processes: [%s]", strings.Join(replacedDefaults, ", ")))
	}
	return nil
}

func validateNoMultipleDefaults(processes []launch.Process) error {
	defaultType := ""
	for _, process := range processes {
		if process.Default && defaultType != "" {
			return fmt.Errorf("multiple default process types aren't allowed")
		}
		if process.Default {
			defaultType = process.Type
		}
	}
	return nil
}

func validateUnmet(unmet []dataformat.Unmet, bpPlan dataformat.Plan) error {
	for _, unmet := range unmet {
		if unmet.Name == "" {
			return errors.New("unmet.name is required")
		}
		found := false
		for _, req := range bpPlan.Entries {
			if unmet.Name == req.Name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unmet.name '%s' must match a requested dependency", unmet.Name)
		}
	}
	return nil
}

func names(requires []dataformat.Require) []string {
	var out []string
	for _, req := range requires {
		out = append(out, req.Name)
	}
	return out
}

func WithBuildpack(bp GroupBuildpack, bom []dataformat.BOMEntry) []dataformat.BOMEntry {
	var out []dataformat.BOMEntry
	for _, entry := range bom {
		entry.Buildpack = dataformat.Buildpack{
			ID:       bp.ID,
			Optional: bp.Optional,
			Version:  bp.Version,
		}
		out = append(out, entry)
	}
	return out
}
