package main

import (
	"fmt"

	"github.com/buildpacks/imgutil"
	"github.com/buildpacks/imgutil/local"
	"github.com/buildpacks/imgutil/remote"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"

	"github.com/buildpacks/lifecycle"
	"github.com/buildpacks/lifecycle/auth"
	"github.com/buildpacks/lifecycle/buildpack"
	"github.com/buildpacks/lifecycle/cmd"
	"github.com/buildpacks/lifecycle/image"
	"github.com/buildpacks/lifecycle/internal/encoding"
	"github.com/buildpacks/lifecycle/internal/layer"
	"github.com/buildpacks/lifecycle/platform"
	"github.com/buildpacks/lifecycle/priv"
)

type analyzeCmd struct {
	analyzeInputs
	platform Platform
	docker   client.CommonAPIClient // construct if necessary before dropping privileges
	keychain authn.Keychain         // construct if necessary before dropping privileges
}

// A superset of flags and arguments defined by all supported platform apis
type analyzeInputs struct {
	additionalTags   cmd.StringSlice // nolint: structcheck
	analyzedPath     string          // nolint: structcheck
	cacheImageRef    string          // nolint: structcheck
	layersDir        string          // nolint: structcheck
	legacyCacheDir   string          // nolint: structcheck
	legacyGroupPath  string          // nolint: structcheck
	outputImageRef   string          // nolint: structcheck
	previousImageRef string          // nolint: structcheck
	runImageRef      string          // nolint: structcheck
	stackPath        string          // nolint: structcheck
	gid              int             // nolint: structcheck
	uid              int             // nolint: structcheck
	legacySkipLayers bool            // nolint: structcheck
	useDaemon        bool            // nolint: structcheck
}

// Inputs needed when run by the creator
type analyzeArgs struct {
	layersDir        string
	previousImageRef string
	runImageRef      string
	legacySkipLayers bool
	useDaemon        bool

	legacyCache lifecycle.Cache
	legacyGroup buildpack.Group
	docker      client.CommonAPIClient
	keychain    authn.Keychain
	platform    Platform
}

// TODO: add comment
func (a *analyzeCmd) DefineFlags() {
	cmd.FlagAnalyzedPath(&a.analyzedPath)
	cmd.FlagCacheImage(&a.cacheImageRef)
	cmd.FlagLayersDir(&a.layersDir)
	cmd.FlagGID(&a.gid)
	cmd.FlagUID(&a.uid)
	cmd.FlagUseDaemon(&a.useDaemon)
	if a.platformAPIVersionGreaterThan06() {
		cmd.FlagPreviousImage(&a.previousImageRef)
		cmd.FlagRunImage(&a.runImageRef)
		cmd.FlagStackPath(&a.stackPath)
		cmd.FlagTags(&a.additionalTags)
	} else {
		cmd.FlagCacheDir(&a.legacyCacheDir)
		cmd.FlagGroupPath(&a.legacyGroupPath)
		cmd.FlagSkipLayers(&a.legacySkipLayers)
	}
}

func (a *analyzeCmd) Args(nargs int, args []string) error {
	// define args
	if nargs != 1 {
		return cmd.FailErrCode(fmt.Errorf("received %d arguments, but expected 1", nargs), cmd.CodeInvalidArgs, "parse arguments")
	}
	a.outputImageRef = args[0]

	// validate args
	if a.outputImageRef == "" {
		return cmd.FailErrCode(errors.New("image argument is required"), cmd.CodeInvalidArgs, "parse arguments")
	}

	// validate flags
	if a.restoresLayerMetadata() {
		if a.cacheImageRef == "" && a.legacyCacheDir == "" {
			cmd.DefaultLogger.Warn("Not restoring cached layer metadata, no cache flag specified.")
		}
	}

	if !a.useDaemon {
		if err := a.ensurePreviousAndTargetHaveSameRegistry(); err != nil {
			return errors.Wrap(err, "ensuring images are on same registry")
		}
	}

	if err := image.ValidateDestinationTags(a.useDaemon, append(a.additionalTags, a.outputImageRef)...); err != nil {
		return cmd.FailErrCode(err, cmd.CodeInvalidArgs, "validate image tag(s)")
	}

	// fill in default values
	if a.analyzedPath == cmd.PlaceholderAnalyzedPath {
		a.analyzedPath = cmd.DefaultAnalyzedPath(a.platform.API().String(), a.layersDir)
	}

	if a.legacyGroupPath == cmd.PlaceholderGroupPath {
		a.legacyGroupPath = cmd.DefaultGroupPath(a.platform.API().String(), a.layersDir)
	}

	if a.previousImageRef == "" {
		a.previousImageRef = a.outputImageRef
	}

	if err := a.populateRunImageIfNeeded(); err != nil {
		return errors.Wrap(err, "populating run image")
	}

	return nil
}

// TODO: move to image?
func parseRegistry(providedRef string) (string, error) {
	ref, err := name.ParseReference(providedRef, name.WeakValidation)
	if err != nil {
		return "", err
	}
	return ref.Context().RegistryStr(), nil
}

func (a *analyzeCmd) Privileges() error {
	var err error
	a.keychain, err = auth.DefaultKeychain(a.registryImages()...)
	if err != nil {
		return cmd.FailErr(err, "resolve keychain")
	}
	if a.useDaemon {
		a.docker, err = priv.DockerClient()
		if err != nil {
			return cmd.FailErr(err, "initialize docker client")
		}
	}
	if a.platformAPIVersionGreaterThan06() { // TODO: this could move to lifecycle package
		if err := image.VerifyRegistryAccess(a, a.keychain); err != nil {
			return cmd.FailErr(err)
		}
	}
	if err := priv.EnsureOwner(a.uid, a.gid, a.layersDir, a.legacyCacheDir); err != nil {
		return cmd.FailErr(err, "chown volumes")
	}
	if err := priv.RunAs(a.uid, a.gid); err != nil {
		return cmd.FailErr(err, fmt.Sprintf("exec as user %d:%d", a.uid, a.gid))
	}
	return nil
}

func (a *analyzeCmd) registryImages() []string {
	var registryImages []string
	registryImages = append(registryImages, a.ReadableRegistryImages()...)
	return append(registryImages, a.WriteableRegistryImages()...)
}

func (a *analyzeCmd) Exec() error {
	analyzeArgs, err := a.newAnalyzeArgs()
	if err != nil {
		return err
	}
	analyzedMD, err := analyzeArgs.analyze()
	if err != nil {
		return err
	}
	if err := encoding.WriteTOML(a.analyzedPath, analyzedMD); err != nil {
		return errors.Wrap(err, "write analyzed.toml")
	}
	return nil
}

func (a *analyzeCmd) newAnalyzeArgs() (*analyzeArgs, error) {
	aa := &analyzeArgs{
		docker:           a.docker,
		keychain:         a.keychain,
		layersDir:        a.layersDir,
		legacySkipLayers: a.legacySkipLayers,
		platform:         a.platform,
		previousImageRef: a.previousImageRef,
		runImageRef:      a.runImageRef,
		useDaemon:        a.useDaemon,
	}
	if a.restoresLayerMetadata() {
		var err error
		if aa.legacyGroup, err = buildpack.ReadGroup(a.legacyGroupPath); err != nil { // TODO: this could move to lifecycle package
			return nil, cmd.FailErr(err, "read buildpack group")
		}
		if err := verifyBuildpackApis(aa.legacyGroup); err != nil {
			return nil, err
		}
		if aa.legacyCache, err = initCache(a.cacheImageRef, a.legacyCacheDir, a.keychain); err != nil {
			return nil, cmd.FailErr(err, "initialize cache")
		}
	}
	return aa, nil
}

func (aa analyzeArgs) analyze() (platform.AnalyzedMetadata, error) {
	previousImage, err := aa.localOrRemote(aa.previousImageRef)
	if err != nil {
		return platform.AnalyzedMetadata{}, cmd.FailErr(err, "get previous image")
	}

	runImage, err := aa.localOrRemote(aa.runImageRef)
	if err != nil {
		return platform.AnalyzedMetadata{}, cmd.FailErr(err, "get previous image")
	}

	analyzedMD, err := (&lifecycle.Analyzer{
		Buildpacks:            aa.legacyGroup.Group,
		Cache:                 aa.legacyCache,
		Logger:                cmd.DefaultLogger,
		Platform:              aa.platform,
		PreviousImage:         previousImage,
		RunImage:              runImage,
		LayerMetadataRestorer: layer.NewMetadataRestorer(cmd.DefaultLogger, aa.layersDir, aa.legacySkipLayers),
		SBOMRestorer:          layer.NewSBOMRestorer(aa.layersDir, cmd.DefaultLogger),
	}).Analyze()
	if err != nil {
		return platform.AnalyzedMetadata{}, cmd.FailErrCode(err, aa.platform.CodeFor(platform.AnalyzeError), "analyzer")
	}

	return analyzedMD, nil
}

func (aa analyzeArgs) localOrRemote(fromImage string) (imgutil.Image, error) {
	if fromImage == "" {
		return nil, nil
	}

	if aa.useDaemon {
		return local.NewImage(
			fromImage,
			aa.docker,
			local.FromBaseImage(fromImage),
		)
	}

	return remote.NewImage(
		fromImage,
		aa.keychain,
		remote.FromBaseImage(fromImage),
	)
}

func (a *analyzeCmd) platformAPIVersionGreaterThan06() bool {
	return a.platform.API().AtLeast("0.7")
}

func (a *analyzeCmd) restoresLayerMetadata() bool {
	return !a.platformAPIVersionGreaterThan06()
}

func (a *analyzeCmd) supportsRunImage() bool {
	return a.platformAPIVersionGreaterThan06()
}

// populateRunImageIfNeeded() finds the best run image mirror using stack metadata and target registry.
func (a *analyzeCmd) populateRunImageIfNeeded() error {
	if !a.supportsRunImage() || a.runImageRef != "" {
		return nil
	}

	targetRegistry, err := parseRegistry(a.outputImageRef)
	if err != nil {
		return err
	}

	stackMD, err := readStack(a.stackPath)
	if err != nil {
		return err
	}

	a.runImageRef, err = stackMD.BestRunImageMirror(targetRegistry)
	if err != nil {
		return errors.New("-run-image is required when there is no stack metadata available")
	}

	return nil
}

func (a *analyzeCmd) ReadableRegistryImages() []string {
	var readableImages []string
	if !a.useDaemon {
		readableImages = appendNotEmpty(readableImages, a.previousImageRef, a.runImageRef)
	}
	return readableImages
}

func (a *analyzeCmd) WriteableRegistryImages() []string {
	var writeableImages []string
	writeableImages = appendNotEmpty(writeableImages, a.cacheImageRef)
	if !a.useDaemon {
		writeableImages = appendNotEmpty(writeableImages, a.outputImageRef)
		writeableImages = appendNotEmpty(writeableImages, a.additionalTags...)
	}
	return writeableImages
}

func (a *analyzeCmd) ensurePreviousAndTargetHaveSameRegistry() error {
	if a.previousImageRef != "" && a.previousImageRef != a.outputImageRef {
		targetRegistry, err := parseRegistry(a.outputImageRef)
		if err != nil {
			return err
		}
		previousRegistry, err := parseRegistry(a.previousImageRef)
		if err != nil {
			return err
		}
		if previousRegistry != targetRegistry {
			return fmt.Errorf("previous image is on a different registry %s from the exported image %s", previousRegistry, targetRegistry)
		}
	}
	return nil
}
