package acquire

type Verification struct {
	DurationSeconds float64 `json:"duration_seconds"`
	BandwidthHz     int     `json:"bandwidth_hz"`
	BandwidthRating string  `json:"bandwidth_rating"`
	PeakDbFS        float64 `json:"peak_dbfs"`
	RMSDbFS         float64 `json:"rms_dbfs"`
	GainOffsetDb    float64 `json:"gain_offset_db"`
	Status          string  `json:"status"`
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
