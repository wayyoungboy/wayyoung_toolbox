package catalog

import "context"

// AutomationConfig controls future organizer receipts, not existing queued
// successors. It never discovers paths or grants additional directory access.
type AutomationConfig struct {
	AfterOrganize bool  `json:"after_organize"`
	Version       int64 `json:"version"`
}

func (s *Store) Automation(ctx context.Context) (AutomationConfig, error) {
	var config AutomationConfig
	err := s.db.QueryRowContext(ctx, "SELECT after_organize,version FROM catalog_automation WHERE id=1").Scan(&config.AfterOrganize, &config.Version)
	return config, err
}

func (s *Store) SetAutomation(ctx context.Context, config AutomationConfig) (AutomationConfig, error) {
	if config.Version < 1 {
		return AutomationConfig{}, ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, "UPDATE catalog_automation SET after_organize=?,version=version+1 WHERE id=1 AND version=?", config.AfterOrganize, config.Version)
	if err != nil {
		return AutomationConfig{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return AutomationConfig{}, err
	}
	if count != 1 {
		return AutomationConfig{}, ErrConflict
	}
	config.Version++
	return config, nil
}
