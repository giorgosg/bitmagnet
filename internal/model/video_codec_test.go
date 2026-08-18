package model

import "testing"

func TestInferVideoCodecAV1(t *testing.T) {
	t.Parallel()

	codec, releaseGroup := InferVideoCodecAndReleaseGroup("Example.Movie.AV1-GROUP")
	if !codec.Valid || codec.VideoCodec.String() != "AV1" {
		t.Fatalf("codec = %#v, want AV1", codec)
	}

	if !releaseGroup.Valid || releaseGroup.String != "GROUP" {
		t.Fatalf("release group = %#v, want GROUP", releaseGroup)
	}
}
