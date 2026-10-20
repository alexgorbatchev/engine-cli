package acquire

// Upstream tools own their diagnostic text. Engine supplies its stable code at
// the decoding boundary, including partial failures in otherwise successful work.
func setFailureCodes(result any) {
	switch r := result.(type) {
	case *TrackResult:
		r.ErrorCode = toolFailureCode(r.Error)
	case *MixResult:
		r.ErrorCode = toolFailureCode(r.Error)
		for i := range r.Tracks {
			r.Tracks[i].ErrorCode = toolFailureCode(r.Tracks[i].Error)
		}
	case *TagResult:
		r.ErrorCode = toolFailureCode(r.Error)
		for i := range r.Results {
			r.Results[i].ErrorCode = toolFailureCode(r.Results[i].Error)
		}
	case *Dependencies:
		r.ErrorCode = toolFailureCode(r.Error)
		for i := range r.Dependencies {
			r.Dependencies[i].ErrorCode = toolFailureCode(r.Dependencies[i].Error)
		}
	case *Upgrade:
		r.ErrorCode = toolFailureCode(r.Error)
	}
}

func toolFailureCode(message string) string {
	if message == "" {
		return ""
	}
	return "tool_failed"
}
