# Autostart Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Bubble Tea autostart editor and move goMH-created autostarts from scheduled tasks to registry Run/RunOnce.

**Architecture:** `core` exposes autostart DTOs through `WinUtils`; `winutils` owns all Windows registry, Startup folder, shortcut, and Task Scheduler integration. `modules/autostart` is an immediate module with a dedicated Bubble Tea model that scans, stages changes, and applies them only when the user saves.

**Tech Stack:** Go 1.25.1, Bubble Tea, Lip Gloss, Windows registry, PowerShell for structured scheduled-task and `.lnk` metadata.

---

### Task 1: Core Autostart Contract

**Files:**
- Modify: `core/types.go`
- Test: `winutils/autostart_test.go`

- [ ] Add autostart DTOs: source, scope, registry key, entry, create request, staged change.
- [ ] Extend `core.WinUtils` with list/apply/create/registry-value helpers.
- [ ] Write failing tests for registry command formatting and PowerShell JSON parsing helpers.
- [ ] Implement the minimal `winutils` helpers and verify tests pass.

### Task 2: Windows Autostart Store

**Files:**
- Create: `winutils/autostart.go`
- Create: `winutils/autostart_test.go`
- Modify: `app/platform/winutils.go`

- [ ] Scan HKCU/HKLM Run and RunOnce values.
- [ ] Scan user/common Startup folders.
- [ ] Scan scheduled tasks with logon/boot triggers through structured PowerShell JSON.
- [ ] Implement enabling/disabling: scheduled tasks use enable/disable, registry and Startup folder entries are deleted when disabled.
- [ ] Implement creation of Run/RunOnce values, including `.lnk` resolution.

### Task 3: Bubble Tea Module

**Files:**
- Create: `modules/autostart/module.go`
- Create: `modules/autostart/ui.go`
- Create: `modules/autostart/ui_test.go`
- Modify: `modules/registry/registry.go`
- Modify: `config.json`

- [ ] Add immediate module `autostart`.
- [ ] Build a Bubble Tea table-like UI with keyboard and mouse navigation.
- [ ] Toggle entries with Space or click.
- [ ] Add entries with `a`, choosing Run or RunOnce, path, and arguments.
- [ ] Keep save inactive until changes exist; save with `s`; exit with Esc/Ctrl+C.
- [ ] Register module and expose it in the module list.

### Task 4: Convert goMH Autostart Call Sites

**Files:**
- Modify: `modules/distro/resume.go`
- Modify: `modules/regime/regime.go`
- Modify: `modules/vcomcaster/vcomcaster.go`
- Modify: `main.go`

- [ ] Replace distro/regime resume scheduled tasks with HKCU RunOnce values.
- [ ] Replace VComCaster scheduled autostart with HKCU Run value.
- [ ] Remove VComCaster autostart by deleting the Run value.
- [ ] Preserve temp cleanup while RunOnce resume values exist.

### Task 5: Verification

**Files:**
- All touched Go files

- [ ] Run `gofmt` on touched Go files.
- [ ] Run targeted package tests.
- [ ] Run `go test ./...`.
- [ ] Run `go build -v -o goMH.exe .`.
