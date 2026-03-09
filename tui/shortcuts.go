package tui

var menuShortcutKeys = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0"}

func menuShortcutKey(index int) string {
	if index < 0 || index >= len(menuShortcutKeys) {
		return ""
	}
	return menuShortcutKeys[index]
}

func menuShortcutLabel(index int) string {
	key := menuShortcutKey(index)
	if key == "" {
		return "   "
	}
	return "[" + key + "]"
}

func menuShortcutOffset(key string) (int, bool) {
	for index, candidate := range menuShortcutKeys {
		if candidate == key {
			return index, true
		}
	}
	return 0, false
}
