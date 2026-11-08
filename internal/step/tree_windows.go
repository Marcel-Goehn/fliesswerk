package step

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tree is a Step's process tree on Windows: a job object with
// kill-on-close that the Harness, and so everything it starts, belongs to.
type tree struct{ job windows.Handle }

// startClient starts a container engine's client, like any Harness here.
func startClient(spawn func(*syscall.SysProcAttr) (*process, error)) (*process, *tree, error) {
	return startTree(spawn)
}

// startTree starts the Harness suspended, puts it in a new job object and
// only then lets it run, so nothing it starts can be outside the job. A
// new process group keeps a terminal's Ctrl-C away from it.
func startTree(spawn func(*syscall.SysProcAttr) (*process, error)) (*process, *tree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil { //nolint:gosec // the Win32 API takes the struct by pointer
		_ = windows.CloseHandle(job)
		return nil, nil, fmt.Errorf("set up job object: %w", err)
	}
	p, err := spawn(&syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP})
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, nil, err
	}
	pid := uint32(p.cmd.Process.Pid) //nolint:gosec // a pid fits in uint32
	if err := assign(job, pid); err != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, nil, fmt.Errorf("put the Harness in a job object: %w", err)
	}
	t := &tree{job: job}
	if err := resume(pid); err != nil {
		t.kill()
		t.close()
		_ = p.cmd.Wait()
		return nil, nil, fmt.Errorf("resume the Harness: %w", err)
	}
	return p, t, nil
}

func assign(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.AssignProcessToJobObject(job, h)
}

// resume resumes the threads of a process started suspended: its main
// thread, the only one it has.
func resume(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	resumed := false
	e := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snap, &e); err == nil; err = windows.Thread32Next(snap, &e) {
		if e.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, e.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(th)
		_ = windows.CloseHandle(th)
		if err != nil {
			return err
		}
		resumed = true
	}
	if !resumed {
		return errors.New("found no thread to resume")
	}
	return nil
}

// kill kills every process in the job.
func (t *tree) kill() { _ = windows.TerminateJobObject(t.job, 1) }

// close closes the job, which kills whatever the Harness left behind.
func (t *tree) close() {
	t.kill()
	_ = windows.CloseHandle(t.job)
}
