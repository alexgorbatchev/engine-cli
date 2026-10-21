// Package libraryops plans library and media changes against known collections.
package libraryops

type Options struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	SharedMode  string `json:"shared_mode"`
	DeleteMedia bool   `json:"delete_media"`
	KeepShared  bool   `json:"keep_shared"`
	DryRun      bool   `json:"dry_run"`
	PlanToken   string `json:"plan_token"`
	ConfirmPath string `json:"confirm_path"`
	Action      string `json:"-"`
}

type Library struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type File struct {
	Path        string    `json:"path"`
	Destination string    `json:"destination,omitempty"`
	Action      string    `json:"action"`
	Libraries   []Library `json:"libraries"`
	Missing     bool      `json:"missing"`
	Hash        string    `json:"hash"`
	IDs         []int64   `json:"-"`
}

type Plan struct {
	Action               string    `json:"action"`
	Path                 string    `json:"path"`
	Destination          string    `json:"destination,omitempty"`
	Applicable           bool      `json:"applicable"`
	Files                []File    `json:"files"`
	AffectedLibraries    []Library `json:"affected_libraries"`
	UnavailableLibraries []Library `json:"unavailable_libraries"`
	Warnings             []string  `json:"warnings"`
	PlanToken            string    `json:"plan_token"`
	databases            map[string]string
}
