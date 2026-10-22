package codediagnostics

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type hostProcess struct {
	cmd *exec.Cmd
	job windows.Handle
}

// Start suspended, assign a kill-on-close job BEFORE any checker code can spawn
// descendants, then resume. Job assignment failure fails closed (no taskkill race).
func startProcess(in Invocation) (Process, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	cmd := exec.Command(in.Executable, in.Args...)
	cmd.Dir, cmd.Env = in.Directory, in.Env
	cmd.Stdout, cmd.Stderr = in.Stdout, in.Stderr
	cmd.WaitDelay = in.CleanupTimeout
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		if err == nil {
			resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
			if err = resume.Find(); err == nil {
				status, _, _ := resume.Call(uintptr(process))
				if status != 0 {
					err = syscall.EINVAL
				}
			}
		}
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = windows.CloseHandle(job)
		_ = cmd.Wait()
		return nil, err
	}
	return &hostProcess{cmd, job}, nil
}

func (p *hostProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
func (p *hostProcess) Terminate() error { return windows.TerminateJobObject(p.job, 1) }
func (p *hostProcess) Close() error     { return windows.CloseHandle(p.job) }
