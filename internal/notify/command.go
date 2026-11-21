package notify

// Banner is the kind name, title and body the native banner for event
// shows. ok is false for a kind that has no banner.
func Banner(event Event) (kind, title, body string, ok bool) {
	detail, ok := describe(event.Kind)
	if !ok {
		return "", "", "", false
	}
	title, body = content(event, detail)
	return kindName(event.Kind), title, body, true
}

func kindName(kind Kind) string {
	switch kind {
	case Waiting:
		return "waiting"
	case Finished:
		return "finished"
	case Errored:
		return "errored"
	}
	return ""
}

// commandBannerRunes keeps a long command, a curl with headers and a URL,
// to about what a banner shows before cutting it off itself.
const commandBannerRunes = 120

var newCommandPresentation = presentation{
	macSound:      "Glass",
	windowsSound:  "ms-winsoundevent:Notification.Default",
	linuxSound:    "dialog-warning",
	linuxUrgency:  "normal",
	linuxIcon:     "dialog-warning",
	linuxCategory: "x-agent-manager.notify-command",
}

// ShowCommand shows the user a hook command about to run for the first
// time. It is set in a file and no screen lists it, so this banner is
// where one added without their knowing comes to light.
func ShowCommand(command string) {
	text := sanitize(command)
	if runes := []rune(text); len(runes) > commandBannerRunes {
		text = string(runes[:commandBannerRunes-1]) + "…"
	}
	deliver("", "New hook command", text, newCommandPresentation)
}
