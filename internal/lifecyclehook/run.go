package lifecyclehook

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const (
	OutputLimit = 2 << 10
	waitDelay   = time.Second
)

// cappedBuffer keeps the start of a hook's output and drops the rest,
// while reporting every write as taken so the pipes keep draining.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

func run(ctx context.Context, argv, env []string, stdin []byte) (string, error) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	cmd.Env = append(slices.DeleteFunc(os.Environ(), inheritedBanner), env...)
	cmd.Stdin = bytes.NewReader(stdin)
	output := &cappedBuffer{limit: OutputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	killTreeOnCancel(cmd)
	cmd.WaitDelay = waitDelay
	err = cmd.Run()
	return output.String(), err
}

// inheritedBanner is a banner variable a manager started from a
// notification hook inherited, which only that notification's hooks get.
func inheritedBanner(kv string) bool {
	for _, key := range []string{"AM_NOTIFY_KIND=", "AM_TITLE=", "AM_BODY="} {
		if strings.HasPrefix(kv, key) {
			return true
		}
	}
	return false
}
