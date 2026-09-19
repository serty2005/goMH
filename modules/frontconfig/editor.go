package frontconfig

import (
	"fmt"
	"goMH/core"
	"goMH/tui"
)

type Config struct {
	Snapshot core.ConfigFileSnapshot
	Updated  []byte
}

func Configure(wu core.WinUtils) (*Config, error) {
	snapshot, err := wu.FindFrontConfig()
	if err != nil {
		return nil, err
	}
	doc, err := Parse(snapshot.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", snapshot.Path, err)
	}
	fields := make([]tui.ConfigEditorField, len(doc.Fields))
	for i, f := range doc.Fields {
		fields[i] = tui.ConfigEditorField{ID: f.ID, Name: f.Name, Value: f.Value, Description: f.Description, Boolean: f.Boolean, Null: f.Null, Nullable: f.Nullable}
	}
	toChanges := func(fields []tui.ConfigEditorField) []Change {
		changes := make([]Change, len(fields))
		for i, f := range fields {
			changes[i] = Change{ID: f.ID, Value: f.Value, Null: f.Null}
		}
		return changes
	}
	edited, save, err := tui.EditConfigFields(fields, tui.ConfigEditorOptions{
		Title:    "Редактор config.xml — " + snapshot.Brand + "Front",
		Subtitle: snapshot.Path + " · " + snapshot.ModTime.Format("02.01.2006 15:04:05"),
		Validate: func(fields []tui.ConfigEditorField) error {
			_, err := doc.Marshal(toChanges(fields))
			return err
		},
	})
	if err != nil || !save || len(edited) == 0 {
		return nil, err
	}
	updated, err := doc.Marshal(toChanges(edited))
	if err != nil {
		return nil, err
	}
	return &Config{Snapshot: snapshot, Updated: updated}, nil
}

func Execute(ctx core.TaskContext, wu core.WinUtils, cfg *Config) error {
	if cfg == nil || cfg.Snapshot.Path == "" {
		return fmt.Errorf("не задан файл для редактирования")
	}
	if _, err := Parse(cfg.Updated); err != nil {
		return err
	}
	if err := ctx.Context().Err(); err != nil {
		return err
	}
	backup, err := wu.SaveFileWithBackup(cfg.Snapshot.Path, cfg.Snapshot.Data, cfg.Updated)
	if err != nil {
		return err
	}
	ctx.Success("Сохранён " + cfg.Snapshot.Path)
	if backup != "" {
		ctx.Info("Резервная копия: " + backup)
	}
	ctx.Info("Настройки будут применены при следующем запуске Front.")
	return nil
}
