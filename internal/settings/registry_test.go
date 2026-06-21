package settings

import "testing"

func TestValidateSettingValue(t *testing.T) {
	tests := map[string]struct {
		key     string
		value   string
		wantErr bool
	}{
		"valid int":          {key: SettingMaxUploadBytes, value: "1024"},
		"negative int":       {key: SettingMaxUploadBytes, value: "-1", wantErr: true},
		"invalid int":        {key: SettingMaxUploadBytes, value: "large", wantErr: true},
		"valid bool":         {key: SettingDatabaseFeature, value: "true"},
		"valid runtime":      {key: SettingRuntimeHTTPFeature, value: "false"},
		"valid memory cap":   {key: SettingRuntimeMemoryMaxBytes, value: "1024"},
		"valid memory wipe":  {key: SettingRuntimeMemoryWipe, value: "true"},
		"invalid bool":       {key: SettingDatabaseFeature, value: "yes", wantErr: true},
		"invalid memory cap": {key: SettingRuntimeMemoryMaxBytes, value: "-1", wantErr: true},
		"valid log level":    {key: SettingLogLevel, value: "warning"},
		"invalid enum":       {key: SettingLogLevel, value: "trace", wantErr: true},
		"valid hosts":        {key: SettingAllowedHosts, value: "*.example.com\nadmin.example.com"},
		"invalid hosts":      {key: SettingAllowedHosts, value: "*example.com", wantErr: true},
		"unknown key":        {key: "unknown", value: "value", wantErr: true},
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

func TestParseAllowedHosts(t *testing.T) {
	hosts, err := ParseAllowedHosts(" *.Example.com,\nadmin.example.com *.example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := FormatAllowedHosts(hosts), "*.example.com\nadmin.example.com"; got != want {
		t.Fatalf("allowed hosts = %q, want %q", got, want)
	}
}
