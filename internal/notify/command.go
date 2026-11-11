package notify

// CommandEnv is the environment a notify hook runs with, on top of the
// hook envelope's own variables. Values carry agent-written text, so they
// travel as variables and never as part of the command line. The hook
// dispatcher keeps only the kind, the title and the body. The rest would
// duplicate the envelope's variables, and a duplicate name is the one the
// process reads first.
func CommandEnv(event Event) []string {
	detail, ok := describe(event.Kind)
	if !ok {
		return nil
	}
	title, body := content(event, detail)
	return []string{
		"AM_NOTIFY_KIND=" + kindName(event.Kind),
		"AM_SESSION_ID=" + event.ID,
		"AM_SESSION_NAME=" + sanitize(event.Session),
		"AM_TOOL=" + sanitize(event.Tool),
		"AM_CWD=" + event.Dir,
		"AM_BRANCH=" + sanitize(event.Branch),
		"AM_TITLE=" + title,
		"AM_BODY=" + body,
	}
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
