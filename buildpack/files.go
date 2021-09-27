package buildpack

import (
	"errors"
	"os"

	"github.com/buildpacks/lifecycle/buildpack/dataformat"
	api05 "github.com/buildpacks/lifecycle/buildpack/v05"
	api06 "github.com/buildpacks/lifecycle/buildpack/v06"
)

type EncoderDecoder interface {
	IsSupported(buildpackAPI string) bool
	Encode(file *os.File, lmf dataformat.LayerMetadataFile) error
	Decode(path string) (dataformat.LayerMetadataFile, string, error)
}

func defaultEncodersDecoders() []EncoderDecoder {
	return []EncoderDecoder{
		// TODO: it's weird that api05 is relevant for buildpack APIs 0.2-0.5 and api06 is relevant for buildpack API 0.6 and up. We should work on it.
		api05.NewEncoderDecoder(),
		api06.NewEncoderDecoder(),
	}
}

func EncodeLayerMetadataFile(lmf dataformat.LayerMetadataFile, path, buildpackAPI string) error {
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()

	encoders := defaultEncodersDecoders()

	for _, encoder := range encoders {
		if encoder.IsSupported(buildpackAPI) {
			return encoder.Encode(fh, lmf)
		}
	}
	return errors.New("couldn't find an encoder")
}

func DecodeLayerMetadataFile(path, buildpackAPI string) (dataformat.LayerMetadataFile, string, error) { // TODO: pass the logger and print the warning inside (instead of returning a message)
	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		return dataformat.LayerMetadataFile{}, "", nil
	} else if err != nil {
		return dataformat.LayerMetadataFile{}, "", err
	}
	defer fh.Close()

	decoders := defaultEncodersDecoders()

	for _, decoder := range decoders {
		if decoder.IsSupported(buildpackAPI) {
			return decoder.Decode(path)
		}
	}
	return dataformat.LayerMetadataFile{}, "", errors.New("couldn't find a decoder")
}
