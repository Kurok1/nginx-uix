/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	linuxProcRoot        = "/proc"
	linuxUserHZ          = 100
	procStatFileLimit    = 8 * 1024
	procCmdlineFileLimit = 64 * 1024
	procSystemStatLimit  = 256 * 1024
	procExecutableLimit  = 4 * 1024
	maxProcEntries       = 65_536
)

type procStat struct {
	PID        int
	Name       string
	State      byte
	ParentPID  int
	StartTicks uint64
}

type processRecord struct {
	PID             int
	Name            string
	State           byte
	ParentPID       int
	StartedAt       time.Time
	Executable      string
	Arguments       []string
	ExecutableError error
	CmdlineError    error
}

type processSnapshot struct {
	Processes  map[int]processRecord
	Unreadable map[int]error
}

func (s processSnapshot) Children(parentPID int) []processRecord {
	children := make([]processRecord, 0)
	for _, process := range s.Processes {
		if process.ParentPID == parentPID {
			children = append(children, process)
		}
	}
	sort.Slice(children, func(left, right int) bool {
		return children[left].PID < children[right].PID
	})
	return children
}

type procFS struct {
	root     string
	readDir  func(string) ([]fs.DirEntry, error)
	readFile func(context.Context, string, int64) ([]byte, error)
	readlink func(string) (string, error)
}

func newLinuxProcessSource() *procFS {
	return &procFS{root: linuxProcRoot}
}

func (p *procFS) Snapshot(ctx context.Context) (processSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return processSnapshot{}, fmt.Errorf("read process snapshot: %w", err)
	}
	root := p.root
	if root == "" {
		root = linuxProcRoot
	}
	readDir := p.readDir
	if readDir == nil {
		readDir = os.ReadDir
	}
	readFile := p.readFile
	if readFile == nil {
		readFile = readBoundedFile
	}
	readlink := p.readlink
	if readlink == nil {
		readlink = os.Readlink
	}

	bootContents, err := readFile(ctx, filepath.Join(root, "stat"), procSystemStatLimit)
	if err != nil {
		return processSnapshot{}, fmt.Errorf("read process boot time: %w", err)
	}
	bootTime, err := parseProcBootTime(bootContents)
	if err != nil {
		return processSnapshot{}, err
	}
	entries, err := readDir(root)
	if err != nil {
		return processSnapshot{}, fmt.Errorf("enumerate process table: %w", err)
	}

	snapshot := processSnapshot{
		Processes:  make(map[int]processRecord),
		Unreadable: make(map[int]error),
	}
	numericEntries := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return processSnapshot{}, fmt.Errorf("read process snapshot: %w", err)
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		numericEntries++
		if numericEntries > maxProcEntries {
			return processSnapshot{}, fmt.Errorf("read process snapshot: process entry limit exceeded")
		}

		process, present, readErr := readProcProcess(ctx, root, pid, bootTime, readFile, readlink)
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return processSnapshot{}, readErr
			}
			snapshot.Unreadable[pid] = readErr
			continue
		}
		if present {
			snapshot.Processes[pid] = process
		}
	}
	return snapshot, nil
}

func readProcProcess(
	ctx context.Context,
	root string,
	pid int,
	bootTime time.Time,
	readFile func(context.Context, string, int64) ([]byte, error),
	readlink func(string) (string, error),
) (processRecord, bool, error) {
	processRoot := filepath.Join(root, strconv.Itoa(pid))
	statContents, err := readFile(ctx, filepath.Join(processRoot, "stat"), procStatFileLimit)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return processRecord{}, false, nil
		}
		return processRecord{}, false, fmt.Errorf("read process %d stat: %w", pid, err)
	}
	stat, err := parseProcStat(statContents)
	if err != nil {
		return processRecord{}, false, fmt.Errorf("read process %d stat: %w", pid, err)
	}
	if stat.PID != pid {
		return processRecord{}, false, fmt.Errorf("read process %d stat: pid changed to %d", pid, stat.PID)
	}
	startedAt, err := procStartTime(bootTime, stat.StartTicks)
	if err != nil {
		return processRecord{}, false, fmt.Errorf("read process %d start time: %w", pid, err)
	}
	process := processRecord{
		PID:       pid,
		Name:      stat.Name,
		State:     stat.State,
		ParentPID: stat.ParentPID,
		StartedAt: startedAt,
	}

	cmdlineContents, err := readFile(ctx, filepath.Join(processRoot, "cmdline"), procCmdlineFileLimit)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return processRecord{}, false, nil
		}
		process.CmdlineError = fmt.Errorf("read cmdline: %w", err)
	} else {
		process.Arguments, err = parseProcCmdline(cmdlineContents)
		if err != nil {
			process.CmdlineError = err
		}
	}
	if err := ctx.Err(); err != nil {
		return processRecord{}, false, fmt.Errorf("read process %d: %w", pid, err)
	}

	process.Executable, err = readlink(filepath.Join(processRoot, "exe"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return processRecord{}, false, nil
		}
		process.ExecutableError = fmt.Errorf("read executable: %w", err)
	} else if len(process.Executable) == 0 || len(process.Executable) > procExecutableLimit {
		process.ExecutableError = fmt.Errorf("read executable: invalid target length")
		process.Executable = ""
	}
	if err := ctx.Err(); err != nil {
		return processRecord{}, false, fmt.Errorf("read process %d: %w", pid, err)
	}
	return process, true, nil
}

func parseProcStat(contents []byte) (procStat, error) {
	line := strings.TrimSpace(string(contents))
	open := strings.IndexByte(line, '(')
	close := strings.LastIndexByte(line, ')')
	if open <= 0 || close <= open || close+1 >= len(line) {
		return procStat{}, fmt.Errorf("parse proc stat: malformed process name")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil || pid <= 0 {
		return procStat{}, fmt.Errorf("parse proc stat: invalid pid")
	}
	name := line[open+1 : close]
	if name == "" {
		return procStat{}, fmt.Errorf("parse proc stat: empty process name")
	}
	fields := strings.Fields(line[close+1:])
	if len(fields) < 20 || len(fields[0]) != 1 {
		return procStat{}, fmt.Errorf("parse proc stat: truncated fields")
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil || parentPID < 0 {
		return procStat{}, fmt.Errorf("parse proc stat: invalid parent pid")
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("parse proc stat: invalid start time")
	}
	return procStat{
		PID:        pid,
		Name:       name,
		State:      fields[0][0],
		ParentPID:  parentPID,
		StartTicks: startTicks,
	}, nil
}

func parseProcCmdline(contents []byte) ([]string, error) {
	if len(contents) == 0 {
		return nil, fmt.Errorf("parse proc cmdline: empty input")
	}
	trimmed := bytes.TrimRight(contents, "\x00")
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("parse proc cmdline: empty input")
	}
	parts := bytes.Split(trimmed, []byte{0})
	arguments := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			return nil, fmt.Errorf("parse proc cmdline: empty argument")
		}
		arguments = append(arguments, strings.ToValidUTF8(string(part), "?"))
	}
	return arguments, nil
}

func parseProcBootTime(contents []byte) (time.Time, error) {
	var bootTime time.Time
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "btime" {
			continue
		}
		if len(fields) != 2 || !bootTime.IsZero() {
			return time.Time{}, fmt.Errorf("parse proc boot time: malformed btime")
		}
		seconds, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || seconds < 0 {
			return time.Time{}, fmt.Errorf("parse proc boot time: invalid btime")
		}
		bootTime = time.Unix(seconds, 0).UTC()
	}
	if bootTime.IsZero() {
		return time.Time{}, fmt.Errorf("parse proc boot time: btime is required")
	}
	return bootTime, nil
}

func procStartTime(bootTime time.Time, ticks uint64) (time.Time, error) {
	seconds := ticks / linuxUserHZ
	nanoseconds := (ticks % linuxUserHZ) * uint64(time.Second/linuxUserHZ)
	const maxInt64 = int64(^uint64(0) >> 1)
	if seconds > uint64(maxInt64) {
		return time.Time{}, fmt.Errorf("start time overflows")
	}
	// #nosec G115 -- the uint64 value is bounded by maxInt64 immediately above.
	secondOffset := int64(seconds)
	if bootTime.Unix() > maxInt64-secondOffset {
		return time.Time{}, fmt.Errorf("start time overflows")
	}
	return time.Unix(bootTime.Unix()+secondOffset, int64(nanoseconds)).UTC(), nil
}

func readBoundedFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("read bounded file: invalid limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read bounded file: %w", err)
	}
	// #nosec G304 -- package-private callers supply only fixed proc, PID, or state paths.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read bounded file: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close bounded file: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read bounded file: %w", err)
	}
	if int64(len(contents)) > limit {
		return nil, fmt.Errorf("read bounded file: size limit exceeded")
	}
	return contents, nil
}
