package winutils

import (
	"testing"

	"goMH/core"
)

func TestBuildAutostartCommandQuotesExecutableAndArguments(t *testing.T) {
	command := buildAutostartCommand(`C:\Program Files\App\app.exe`, `--profile "front office"`)

	want := `"C:\Program Files\App\app.exe" --profile "front office"`
	if command != want {
		t.Fatalf("command = %q, want %q", command, want)
	}
}

func TestParseScheduledAutostartTasksKeepsLogonAndBootTriggers(t *testing.T) {
	input := `[
  {
    "TaskName": "Updater",
    "TaskPath": "\\Vendor\\",
    "State": "Ready",
    "Actions": [
      {
        "Execute": "C:\\Tools\\updater.exe",
        "Arguments": "--silent",
        "WorkingDirectory": "C:\\Tools"
      }
    ],
    "Triggers": [
      {"TriggerType": "LogonTrigger"}
    ]
  },
  {
    "TaskName": "Maintenance",
    "TaskPath": "\\",
    "State": "Disabled",
    "Actions": [
      {
        "Execute": "C:\\Tools\\maint.exe",
        "Arguments": "",
        "WorkingDirectory": "C:\\Tools"
      }
    ],
    "Triggers": [
      {"TriggerType": "TimeTrigger"}
    ]
  },
  {
    "TaskName": "BootAgent",
    "TaskPath": "\\",
    "State": "Disabled",
    "Actions": [
      {
        "Execute": "C:\\Tools\\boot.exe",
        "Arguments": "",
        "WorkingDirectory": "C:\\Tools"
      }
    ],
    "Triggers": [
      {"TriggerType": "BootTrigger"}
    ]
  }
]`

	entries, err := parseScheduledAutostartTasks(input)
	if err != nil {
		t.Fatalf("parseScheduledAutostartTasks returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Name != `\Vendor\Updater` || !entries[0].Enabled {
		t.Fatalf("first entry = %#v", entries[0])
	}
	if entries[1].Name != `\BootAgent` || entries[1].Enabled {
		t.Fatalf("second entry = %#v", entries[1])
	}
	if entries[0].Source != core.AutostartSourceScheduledTask {
		t.Fatalf("source = %q, want scheduled task", entries[0].Source)
	}
}

func TestBuildApplyAutostartCommands(t *testing.T) {
	change := core.AutostartChange{
		Entry: core.AutostartEntry{
			ID:      "task|Updater",
			Name:    `\Vendor\Updater`,
			Source:  core.AutostartSourceScheduledTask,
			Enabled: true,
		},
		Enabled: false,
	}

	commands := buildAutostartApplyCommands([]core.AutostartChange{change})
	if len(commands) != 1 {
		t.Fatalf("len(commands) = %d, want 1", len(commands))
	}
	if commands[0].Name != "schtasks" {
		t.Fatalf("command name = %q, want schtasks", commands[0].Name)
	}
	wantArgs := []string{"/Change", "/TN", `\Vendor\Updater`, "/Disable"}
	for i, want := range wantArgs {
		if commands[0].Args[i] != want {
			t.Fatalf("arg[%d] = %q, want %q", i, commands[0].Args[i], want)
		}
	}
}

func TestReportAutostartProgress(t *testing.T) {
	var got []core.AutostartScanProgress

	reportAutostartProgress(func(progress core.AutostartScanProgress) {
		got = append(got, progress)
	}, "Планировщик задач", 7)

	if len(got) != 1 {
		t.Fatalf("len(progress) = %d, want 1", len(got))
	}
	if got[0].Area != "Планировщик задач" {
		t.Fatalf("area = %q", got[0].Area)
	}
	if got[0].Found != 7 {
		t.Fatalf("found = %d, want 7", got[0].Found)
	}
}
