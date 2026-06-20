package settings

import "testing"

func TestValidateSettingValue(t *testing.T) {
	tests := map[string]struct {
		key     string
		value   string
		wantErr bool
	}{
		"valid int":       {key: SettingMaxUploadBytes, value: "1024"},
		"negative int":    {key: SettingMaxUploadBytes, value: "-1", wantErr: true},
		"invalid int":     {key: SettingMaxUploadBytes, value: "large", wantErr: true},
		"valid bool":      {key: SettingDatabaseFeature, value: "true"},
		"valid runtime":   {key: SettingRuntimeHTTPFeature, value: "false"},
		"invalid bool":    {key: SettingDatabaseFeature, value: "yes", wantErr: true},
		"valid log level": {key: SettingLogLevel, value: "warning"},
		"invalid enum":    {key: SettingLogLevel, value: "trace", wantErr: true},
		"unknown key":     {key: "unknown", value: "value", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := Validate(tc.key, tc.value)
			if tc.wantErr && err == nil {
				t.Fatal("Validate returned nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate returned error: %v", err)
			}
		})
	}
}

func TestParseSettingHelpers(t *testing.T) {
	if got := ParseLogLevel(" warning "); got != "warn" {
		t.Fatalf("ParseLogLevel = %q, want warn", got)
	}
	if !ParseBool("true") {
		t.Fatal("ParseBool true = false")
	}
	if got := Default(SettingMaxUploadFiles); got != "10000" {
		t.Fatalf("Default max upload files = %q, want 10000", got)
	}
}
