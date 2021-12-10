package main

import (
	"errors"
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/buildpacks/imgutil"
	"github.com/buildpacks/imgutil/local"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/buildpacks/lifecycle"
	"github.com/buildpacks/lifecycle/auth"
	"github.com/buildpacks/lifecycle/buildpack"
	"github.com/buildpacks/lifecycle/cache"
	"github.com/buildpacks/lifecycle/cmd"
	"github.com/buildpacks/lifecycle/internal/layer"
	"github.com/buildpacks/lifecycle/platform"
	"github.com/buildpacks/lifecycle/priv"
)

type restoreCmd struct {
	restoreArgs
	analyzedPath  string
	cacheDir      string
	cacheImageTag string
	groupPath     string
	uid, gid      int
}

// restoreArgs contains inputs needed when run by creator.
type restoreArgs struct {
	launchCacheDir string
	layersDir      string
	platform       Platform
	skipLayers     bool
	useDaemon      bool

	docker   client.CommonAPIClient // construct if necessary before dropping privileges
	keychain authn.Keychain         // construct if necessary before dropping privileges
}

// DefineFlags defines the flags that are considered valid and reads their values (if provided).
func (r *restoreCmd) DefineFlags() {
	cmd.FlagCacheDir(&r.cacheDir)
	cmd.FlagCacheImage(&r.cacheImageTag)
	cmd.FlagGroupPath(&r.groupPath)
	cmd.FlagLayersDir(&r.layersDir)
	cmd.FlagUID(&r.uid)
	cmd.FlagGID(&r.gid)
	if r.restoresLayerMetadata() {
		cmd.FlagAnalyzedPath(&r.analyzedPath)
		cmd.FlagSkipLayers(&r.skipLayers)
	}
	if r.restoresAppSBOM() {
		cmd.FlagUseDaemon(&r.useDaemon)
		cmd.FlagLaunchCacheDir(&r.launchCacheDir)
	}
}

// Args validates arguments and flags, and fills in default values.
func (r *restoreCmd) Args(nargs int, args []string) error {
	if nargs > 0 {
		return cmd.FailErrCode(errors.New("received unexpected Args"), cmd.CodeInvalidArgs, "parse arguments")
	}

	if r.cacheImageTag == "" && r.cacheDir == "" {
		cmd.DefaultLogger.Warn("Not restoring cached layer data, no cache flag specified.")
	}

	if r.groupPath == cmd.PlaceholderGroupPath {
		r.groupPath = cmd.DefaultGroupPath(r.platform.API().String(), r.layersDir)
	}

	if r.analyzedPath == cmd.PlaceholderAnalyzedPath {
		r.analyzedPath = cmd.DefaultAnalyzedPath(r.platform.API().String(), r.layersDir)
	}

	return nil
}

func (r *restoreCmd) Privileges() error {
	var err error
	r.keychain, err = auth.DefaultKeychain(r.registryImages()...)
	if err != nil {
		return cmd.FailErr(err, "resolve keychain")
	}

	if err := priv.EnsureOwner(r.uid, r.gid, r.layersDir, r.cacheDir); err != nil {
		return cmd.FailErr(err, "chown volumes")
	}
	if err := priv.RunAs(r.uid, r.gid); err != nil {
		return cmd.FailErr(err, fmt.Sprintf("exec as user %d:%d", r.uid, r.gid))
	}
	return nil
}

func (r *restoreCmd) Exec() error {
	group, err := buildpack.ReadGroup(r.groupPath)
	if err != nil {
		return cmd.FailErr(err, "read buildpack group")
	}
	if err := verifyBuildpackApis(group); err != nil {
		return err
	}
	cacheStore, err := initCache(r.cacheImageTag, r.cacheDir, r.keychain)
	if err != nil {
		return err
	}

	var analyzedMD platform.AnalyzedMetadata
	if r.restoresLayerMetadata() {
		_, err = toml.DecodeFile(r.analyzedPath, &analyzedMD)
		if err != nil {
			cmd.DefaultLogger.Infof("Error decoding previous image metadata: %s", err.Error()) // TODO: confirm we want to ignore this error
		}
		cmd.DefaultLogger.Debugf("Analyzed metadata: %+v", analyzedMD)
	}

	return r.restore(analyzedMD, group, cacheStore)
}

func (r *restoreCmd) registryImages() []string {
	if r.cacheImageTag != "" {
		return []string{r.cacheImageTag}
	}
	return []string{}
}

func (r restoreArgs) restoresAppSBOM() bool {
	return r.platform.API().AtLeast("0.9")
}

func (r restoreArgs) restore(analyzedMD platform.AnalyzedMetadata, group buildpack.Group, cacheStore lifecycle.Cache) error {
	var previousImage imgutil.Image
	if r.restoresAppSBOM() && analyzedMD.PreviousImage != nil && analyzedMD.PreviousImage.Reference != "" { // TODO: put in helper function maybe
		var err error
		if r.useDaemon {
			previousImage, err = r.initDaemonAppImage(analyzedMD)
			if err != nil {
				return cmd.FailErrCode(err, r.platform.CodeFor(platform.RestoreError), "initialize previous image")
			}
		} else {
			fmt.Println("TODO") // TODO
		}
	}
	restorer := &lifecycle.Restorer{
		Buildpacks:            group.Group,
		LayerMetadataRestorer: layer.NewMetadataRestorer(cmd.DefaultLogger, r.layersDir, r.skipLayers),
		LayersDir:             r.layersDir,
		LayersMetadata:        analyzedMD.Metadata,
		Logger:                cmd.DefaultLogger,
		Platform:              r.platform,
		PreviousImage:         previousImage,
		SBOMRestorer:          layer.NewSBOMRestorer(r.layersDir, cmd.DefaultLogger),
	}

	if err := restorer.Restore(cacheStore); err != nil {
		return cmd.FailErrCode(err, r.platform.CodeFor(platform.RestoreError), "restore")
	}
	return nil
}

func (r restoreArgs) initDaemonAppImage(analyzedMD platform.AnalyzedMetadata) (imgutil.Image, error) {
	var opts = []local.ImageOption{
		local.FromBaseImage(analyzedMD.RunImage.Reference), // TODO: pick up here by inserting a valid run image into the fixture
	}

	if analyzedMD.PreviousImage != nil {
		cmd.DefaultLogger.Debugf("Reusing layers from image with id '%s'", analyzedMD.PreviousImage.Reference)
		opts = append(opts, local.WithPreviousImage(analyzedMD.PreviousImage.Reference))
	}

	var previousImage imgutil.Image
	previousImage, err := local.NewImage(
		"previous-image", // TODO
		r.docker,
		opts...,
	)
	if err != nil {
		return nil, cmd.FailErr(err, " image")
	}

	if r.launchCacheDir != "" {
		volumeCache, err := cache.NewVolumeCache(r.launchCacheDir)
		if err != nil {
			return nil, cmd.FailErr(err, "create launch cache")
		}
		previousImage = cache.NewCachingImage(previousImage, volumeCache)
	}
	return previousImage, nil
}

func (r *restoreArgs) restoresLayerMetadata() bool {
	return r.platform.API().AtLeast("0.7")
}
