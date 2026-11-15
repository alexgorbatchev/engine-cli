package acquire

// Verification is Fetch Track's quality report for one file.
type Verification struct {
	DurationSeconds float64 `json:"duration_seconds"`
	// Codec is the ffprobe codec name of the measured audio stream.
	Codec string `json:"codec,omitempty"`
	// BitrateBps is the stream's average bitrate, measured from its packets
	// for codecs with a bitrate floor; omitted when unknown.
	BitrateBps      int    `json:"bitrate_bps,omitempty"`
	BandwidthHz     int    `json:"bandwidth_hz"`
	BandwidthRating string `json:"bandwidth_rating"`
	// MinBandwidthHz is the minimum the verdict applied.
	MinBandwidthHz int `json:"min_bandwidth_hz,omitempty"`
	// MinBitrateBps is the codec's bitrate floor at that minimum; omitted when
	// none applies.
	MinBitrateBps int `json:"min_bitrate_bps,omitempty"`
	// MeetsMinimum is the verdict: the cutoff reaches MinBandwidthHz and the
	// bitrate reaches any MinBitrateBps. Nil when the tool reported none.
	MeetsMinimum *bool `json:"meets_minimum,omitempty"`
	// LowQualityReasons names each failed criterion with a stable code:
	// bandwidth_below_minimum when the cutoff is below MinBandwidthHz, and
	// bitrate_below_minimum when the average bitrate is below MinBitrateBps or
	// could not be measured (Opus needs 160 kbps at minimums of 19 kHz and
	// above, because its cutoff does not reflect its bitrate). Empty when the
	// audio meets the minimum.
	LowQualityReasons []string `json:"low_quality_reasons,omitempty"`
	PeakDbFS          float64  `json:"peak_dbfs"`
	RMSDbFS           float64  `json:"rms_dbfs"`
	GainOffsetDb      float64  `json:"gain_offset_db"`
	Status            string   `json:"status"`
}
type Metadata struct {
	Title                string `json:"title,omitempty"`
	Artist               string `json:"artist,omitempty"`
	Album                string `json:"album,omitempty"`
	Year                 string `json:"year,omitempty"`
	Source               string `json:"source,omitempty"`
	CoverArtURL          string `json:"cover_art_url,omitempty"`
	FingerprintConfirmed bool   `json:"fingerprint_confirmed,omitempty"`
}
type Candidate struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Duration     float64 `json:"duration"`
	Source       string  `json:"source"`
	Uploader     string  `json:"uploader,omitempty"`
	Channel      string  `json:"channel,omitempty"`
	WebpageURL   string  `json:"webpage_url"`
	Score        int     `json:"score,omitempty"`
	ScoreReason  string  `json:"score_reason,omitempty"`
	BandwidthHz  int     `json:"bandwidth_hz,omitempty"`
	PeakDbFS     float64 `json:"peak_dbfs,omitempty"`
	RMSDbFS      float64 `json:"rms_dbfs,omitempty"`
	QualityScore int     `json:"quality_score,omitempty"`
}
type TrackResult struct {
	Target            string        `json:"target"`
	Status            string        `json:"status"`
	Error             string        `json:"error,omitempty"`
	ErrorCode         string        `json:"error_code,omitempty"`
	CandidateCount    int           `json:"candidate_count,omitempty"`
	SelectedCandidate *Candidate    `json:"selected_candidate,omitempty"`
	Candidates        []Candidate   `json:"candidates,omitempty"`
	OutputFile        string        `json:"output_file,omitempty"`
	Verification      *Verification `json:"verification,omitempty"`
	Metadata          *Metadata     `json:"metadata,omitempty"`
}
