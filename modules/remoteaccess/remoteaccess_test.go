package remoteaccess

import (
	"errors"
	"testing"

	"goMH/tui"
)

func TestConfigFromChoiceRustDeskStoresPassword(t *testing.T) {
	module := Module{
		promptRustDeskPassword: func() (bool, string, error) {
			return true, "  secret  ", nil
		},
	}

	cfg, err := module.configFromChoice(2)
	if err != nil {
		t.Fatalf("ожидалась успешная конфигурация RustDesk, получена ошибка: %v", err)
	}
	if cfg == nil {
		t.Fatal("ожидался конфиг RustDesk, получен nil")
	}
	if cfg.Tool != ToolRustDesk {
		t.Fatalf("ожидался инструмент RustDesk, получено: %v", cfg.Tool)
	}
	if !cfg.SetPassword {
		t.Fatal("ожидался флаг установки пароля")
	}
	if cfg.Password != "secret" {
		t.Fatalf("ожидался нормализованный пароль secret, получено %q", cfg.Password)
	}
}

func TestConfigFromChoiceRustDeskCancelReturnsNilConfig(t *testing.T) {
	module := Module{
		promptRustDeskPassword: func() (bool, string, error) {
			return false, "", tui.ErrExitToMainMenu
		},
	}

	cfg, err := module.configFromChoice(2)
	if err != nil {
		t.Fatalf("отмена пароля не должна возвращать ошибку, получено: %v", err)
	}
	if cfg != nil {
		t.Fatalf("при отмене ожидался nil-конфиг, получено: %+v", cfg)
	}
}

func TestConfigFromChoiceRustDeskPropagatesPromptError(t *testing.T) {
	expectedErr := errors.New("ошибка ввода")
	module := Module{
		promptRustDeskPassword: func() (bool, string, error) {
			return false, "", expectedErr
		},
	}

	cfg, err := module.configFromChoice(2)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("ожидалась ошибка %v, получено %v", expectedErr, err)
	}
	if cfg != nil {
		t.Fatalf("при ошибке ожидался nil-конфиг, получено: %+v", cfg)
	}
}
