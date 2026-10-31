package intake

import "time"

type WatchState struct {
	Revision  int64       `json:"revision"`
	Suspended bool        `json:"suspended"`
	Reason    string      `json:"reason"`
	Manager   string      `json:"manager"`
	ErrorCode string      `json:"error_code,omitempty"`
	Roots     []WatchRoot `json:"roots"`
}

type WatchRoot struct {
	ID         string     `json:"id"`
	Path       string     `json:"path"`
	Recursive  bool       `json:"recursive"`
	Enabled    bool       `json:"enabled"`
	Phase      string     `json:"phase"`
	LastScanAt *time.Time `json:"last_scan_at"`
	ErrorCode  string     `json:"error_code,omitempty"`
	Observed   int64      `json:"observed"`
	Pending    int64      `json:"pending"`
	Linked     int64      `json:"linked"`
}

type WatchScan struct {
	Completed  bool       `json:"completed"`
	Roots      int64      `json:"roots"`
	Observed   int64      `json:"observed"`
	Queued     int64      `json:"queued"`
	Associated int64      `json:"associated"`
	Pending    int64      `json:"pending"`
	State      WatchState `json:"state"`
}

type WatchAddOptions struct {
	Path      string
	Recursive bool
	Revision  int64
}

type WatchRootOptions struct {
	ID       string
	Revision int64
}
