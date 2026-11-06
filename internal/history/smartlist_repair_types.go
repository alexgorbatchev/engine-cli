package history

// SmartlistRepairRow describes one reviewed native saved-playlist repair.
type SmartlistRepairRow struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	Action        string `json:"action"`
	DeleteEntryID string `json:"delete_entry_id,omitempty"`
}

// SmartlistRepairReview binds consent to the observed collection and history.
type SmartlistRepairReview struct {
	PlanToken      string               `json:"plan_token"`
	Reconnected    int                  `json:"reconnected"`
	Removed        int                  `json:"removed"`
	AmendedDeletes int                  `json:"amended_deletes"`
	Rows           []SmartlistRepairRow `json:"rows"`
}

// SmartlistRepairResult retains the reviewed impact after durable publication.
type SmartlistRepairResult struct {
	SmartlistRepairReview
	Applied bool   `json:"applied"`
	UndoID  string `json:"undo_id,omitempty"`
}

// SmartlistRepairRecovery reports explicit rollback of an interrupted repair.
type SmartlistRepairRecovery struct {
	RolledBack      bool `json:"rolled_back"`
	RestoredTargets int  `json:"restored_targets"`
}
