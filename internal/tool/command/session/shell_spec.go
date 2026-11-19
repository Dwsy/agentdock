package session

// ShellSpec describes the interpreter used to run an exec_command. An empty
// Path falls back to the platform default (bash on unix, PowerShell on Windows).
// Args default to []string{"-c"} on unix; Windows ignores them.
type ShellSpec struct {
	Path string
	Args []string
}

// shellArgs returns the flag list used to pass a command string to the shell.
// An empty Args means the platform default.
func (s ShellSpec) shellArgs() []string {
	if len(s.Args) == 0 {
		return []string{"-c"}
	}
	return s.Args
}
