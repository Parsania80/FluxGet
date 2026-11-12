package media

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
)

func TestMediaJobKeepsOnlySelectedFormatsAndChecksSource(t *testing.T) {
	v, err := parseVideo([]byte(fixtureMetadata))
	if err != nil {
		t.Fatal(err)
	}
	j, err := NewJob(v, Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, filepath.Join(t.TempDir(), "tools.json"))
	if err != nil || len(j.Video.Formats) != 2 {
		t.Fatal("invalid persisted selection", err)
	}
	encoded, _ := json.Marshal(j)
	var restored Job
	if err := json.Unmarshal(encoded, &restored); err != nil || restored.Validate() != nil {
		t.Fatal("job contract cannot round trip", err)
	}
	if !j.Matches(v) {
		t.Fatal("unchanged source rejected")
	}
	for _, mutate := range []func(*Video){
		func(v *Video) { v.ID = "BBBBBBBBBBB" },
		func(v *Video) { v.Duration = 30 },
		func(v *Video) { v.Formats[0].Height = 720 },
		func(v *Video) { v.Formats[1].AudioCodec = "opus" },
	} {
		changed := v
		changed.Formats = append([]Format(nil), v.Formats...)
		mutate(&changed)
		if j.Matches(changed) {
			t.Fatal("changed representation accepted")
		}
	}
	j.Manifest = "relative.json"
	if j.Validate() == nil {
		t.Fatal("relative tool manifest accepted")
	}
	j.Manifest = filepath.Join(t.TempDir(), "tools.json")
	j.Video.Duration = math.Inf(1)
	if j.Validate() == nil {
		t.Fatal("unbounded duration accepted")
	}
}

func TestInspectionRejectsWrongSelectedQualityAndCodec(t *testing.T) {
	v, err := parseVideo([]byte(fixtureMetadata))
	if err != nil {
		t.Fatal(err)
	}
	selection := Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}
	i := Inspection{Streams: []Stream{{Kind: "video", Codec: "h264", Height: 1080, FrameRate: "30/1"}, {Kind: "audio", Codec: "aac"}}}
	if err := i.matchesSelection(v, selection); err != nil {
		t.Fatal(err)
	}
	i.Streams[0].Height = 720
	if i.matchesSelection(v, selection) == nil {
		t.Fatal("wrong resolution accepted")
	}
	i.Streams[0].Height = 1080
	i.Streams[1].Codec = "opus"
	if i.matchesSelection(v, selection) == nil {
		t.Fatal("wrong audio codec accepted")
	}
	i.Streams[1].Codec = "aac"
	v.Formats[0].FPS = 60
	if i.matchesSelection(v, selection) == nil {
		t.Fatal("wrong frame rate accepted")
	}
}
