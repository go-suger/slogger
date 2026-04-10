package slogger

type RotationConfig struct {
	Enabled    bool `toml:"enabled" json:"enabled"`
	MaxSizeMB  int  `toml:"max_size_mb" json:"max_size_mb"`
	MaxBackups int  `toml:"max_backups" json:"max_backups"`
	MaxAgeDays int  `toml:"max_age_days" json:"max_age_days"`
	Compress   bool `toml:"compress" json:"compress"`
	LocalTime  bool `toml:"local_time" json:"local_time"`
}
