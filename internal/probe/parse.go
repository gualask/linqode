package probe

import "strings"

// Parse reads the output of Command. Nothing here fails: a section that is
// missing, empty or unforeseen leaves its field Unknown, and Unknown means
// carry on. A host that answered nothing at all yields the zero Result, which
// claims nothing and turns nothing off.
//
// composeDir is the path Command was built with, so the result carries what it
// is talking about and can say "unconfigured" apart from "not there".
func Parse(raw []byte, composeDir string) Result {
	sections := split(string(raw))
	result := Result{DirectoryPath: composeDir}

	// A section that is *absent* is a host that never got to that part of the
	// batch — a dropped connection, a shell that died — and is not the same as
	// a section that is present and empty, which is a real answer meaning "no,
	// there is none". Only the second turns anything off; the first stays
	// Unknown, and Unknown carries on.
	if dockerPath, asked := sections[dockerMarker]; asked {
		result.Docker, result.DaemonVersion, result.DaemonMessage =
			classifyDaemon(strings.TrimSpace(dockerPath), sections[daemonMarker])
	}
	if plugin, asked := sections[composeMarker]; asked {
		result.Compose, result.ComposeVersion =
			classifyCompose(plugin, sections[legacyMarker])
	}
	if dir, asked := sections[dirMarker]; asked {
		result.Directory = classifyDirectory(composeDir, dir)
	}
	result.OS = prettyName(sections[osMarker])
	return result
}

// classifyDaemon turns `docker version` into the four states worth telling
// apart. A version anywhere in the output wins over everything else: the CLI
// prints warnings on stderr, which this section deliberately captures, and a
// host that answered its version is a host whose daemon answered whatever else
// it also said.
func classifyDaemon(dockerPath, section string) (Docker, string, string) {
	if dockerPath == "" {
		// No binary. Anything the section holds is the shell's, not docker's.
		return DockerAbsent, "", ""
	}
	var message string
	for line := range strings.Lines(section) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isVersion(line) {
			return DockerReady, line, ""
		}
		if message == "" {
			// The first line, not the last: docker leads with the diagnosis
			// and follows it with hints, which is the opposite of a shell
			// pipeline, where the last line is the one that failed.
			message = line
		}
	}
	switch {
	case message == "":
		return DockerUnknown, "", ""
	case strings.Contains(strings.ToLower(message), "permission denied"):
		return DockerDenied, "", message
	default:
		return DockerUnreachable, "", message
	}
}

// classifyCompose prefers the plugin, which is the only one this supports.
// The standalone binary is reported so the screen can name it; a host with
// both is a host with v2.
func classifyCompose(plugin, legacy string) (Compose, string) {
	for line := range strings.Lines(plugin) {
		if line = strings.TrimSpace(line); isVersion(line) {
			return ComposeV2, line
		}
	}
	if strings.TrimSpace(legacy) != "" {
		return ComposeLegacy, ""
	}
	return ComposeAbsent, ""
}

func classifyDirectory(composeDir, section string) Directory {
	if composeDir == "" {
		return DirectoryUnconfigured
	}
	if strings.TrimSpace(section) == dirPresentWord {
		return DirectoryPresent
	}
	return DirectoryMissing
}

// isVersion reports whether a line is a bare version and nothing else. Both
// tools print one on success and prose on failure, and prose has spaces in it.
// The leading `v` is accepted because compose has printed both forms.
func isVersion(line string) bool {
	if line == "" || strings.ContainsAny(line, " \t") {
		return false
	}
	digits := strings.TrimPrefix(line, "v")
	return digits != "" && digits[0] >= '0' && digits[0] <= '9'
}

// prettyName reads the one line of /etc/os-release worth showing. The value is
// shell-quoted in that file by convention but not always in practice, so the
// quotes are stripped if they are there and not required.
func prettyName(section string) string {
	for line := range strings.Lines(section) {
		_, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		return strings.Trim(value, `"'`)
	}
	return ""
}

// split cuts the output into its marked sections.
//
// The same shape as the host batch's splitter, which is deliberate — this is
// that format — but not the same function: sharing twenty lines would mean a
// dependency between two packages that otherwise have nothing to say to each
// other, or a package existing only to hold them. Extract it if a third
// batch appears.
func split(raw string) map[string]string {
	sections := map[string]string{}
	current := ""
	var body strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = body.String()
		}
		body.Reset()
	}
	for line := range strings.Lines(raw) {
		if marker := strings.TrimSpace(line); strings.HasPrefix(marker, "#") {
			flush()
			current = marker
			continue
		}
		body.WriteString(line)
	}
	flush()
	return sections
}
