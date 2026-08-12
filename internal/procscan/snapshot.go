package procscan

import "os"

type OpenFile struct {
	FD   int
	Path string
}

type Listener struct {
	Network string
	Address string
	Port    int
}

type Process struct {
	PID        int
	PPID       int
	UID        uint32
	StartTicks uint64
	Comm       string
	Exe        string
	Cwd        string
	Args       []string
	Env        map[string]string
	Files      []OpenFile
	Listeners  []Listener
}

type Snapshot struct {
	UID       uint32
	Processes []Process
}

func (s Snapshot) HasPID(pid int) bool {
	for _, p := range s.Processes {
		if p.PID == pid {
			return true
		}
	}
	return false
}

type Source interface {
	Snapshot() (Snapshot, error)
}

type ProcFS struct {
	Root string
	UID  uint32
}

func NewProcFS() *ProcFS {
	return &ProcFS{Root: "/proc", UID: uint32(os.Geteuid())}
}
