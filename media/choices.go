package media

// Validate checks a selection against the analyzed source without starting work.
func (s Selection) Validate(v Video) error {
	_, err := s.validate(v)
	return err
}

// OutputFormats returns only formats usable for the requested output. Muxed
// video entries include their own audio; separate video entries need one of the
// returned audio tracks. Compatibility stays identical to Download validation.
func (v Video) OutputFormats(container string, audioOnly bool) (videos, audios []Format) {
	for _, f := range v.Formats {
		if !formatID.MatchString(f.ID) || f.DRM || !supportedProtocol(f.Protocol) {
			continue
		}
		if f.VideoCodec == "none" && f.AudioCodec != "" && f.AudioCodec != "none" {
			if audioOnly {
				s := Selection{AudioFormat: f.ID, Container: container, MP3Kbps: 192}
				if s.Validate(Video{Formats: []Format{f}}) == nil {
					audios = append(audios, f)
				}
			} else if audioCompatible(container, f.AudioCodec) {
				audios = append(audios, f)
			}
		}
	}
	if audioOnly {
		return nil, audios
	}
	for _, f := range v.Formats {
		s := Selection{VideoFormat: f.ID, Container: container}
		formats := []Format{f}
		if f.AudioCodec == "none" && len(audios) > 0 {
			s.AudioFormat = audios[0].ID
			formats = append(formats, audios[0])
		}
		if s.Validate(Video{Formats: formats}) == nil {
			videos = append(videos, f)
		}
	}
	return videos, audios
}
