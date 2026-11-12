package media

import "testing"

func TestOutputFormatsShareDownloadValidation(t *testing.T) {
	v, err := parseVideo([]byte(fixtureMetadata))
	if err != nil {
		t.Fatal(err)
	}
	v.Formats = append(v.Formats,
		Format{ID: "18", VideoCodec: "avc1", AudioCodec: "mp4a.40.2", Protocol: "https", Height: 360},
		Format{ID: "web", VideoCodec: "vp9", AudioCodec: "none", Protocol: "https", Height: 720},
		Format{ID: "unsafe", VideoCodec: "avc1", AudioCodec: "mp4a", Protocol: "m3u8", Height: 720},
		Format{ID: "locked", VideoCodec: "avc1", AudioCodec: "mp4a", Protocol: "https", DRM: true},
	)
	for _, tc := range []struct {
		container      string
		audio          bool
		videos, audios int
	}{{"mp4", false, 2, 1}, {"webm", false, 1, 1}, {"mkv", false, 3, 2}, {"m4a", true, 0, 1}, {"opus", true, 0, 1}, {"mp3", true, 0, 2}, {"invalid", false, 0, 0}} {
		videos, audios := v.OutputFormats(tc.container, tc.audio)
		if len(videos) != tc.videos || len(audios) != tc.audios {
			t.Fatalf("%s audio=%v: got %d/%d", tc.container, tc.audio, len(videos), len(audios))
		}
		if tc.audio {
			for _, a := range audios {
				if err := (Selection{AudioFormat: a.ID, Container: tc.container, MP3Kbps: 192}).Validate(v); err != nil {
					t.Fatal("offered unusable audio", err)
				}
			}
		} else {
			for _, f := range videos {
				s := Selection{VideoFormat: f.ID, Container: tc.container}
				if f.AudioCodec == "none" {
					s.AudioFormat = audios[0].ID
				}
				if err := s.Validate(v); err != nil {
					t.Fatal("offered unusable video", err)
				}
			}
		}
	}
	// A source with video-only tracks and no usable audio must not be offered.
	v.Formats = v.Formats[:1]
	if videos, _ := v.OutputFormats("mp4", false); len(videos) != 0 {
		t.Fatal("offered video without a compatible audio track")
	}
}
