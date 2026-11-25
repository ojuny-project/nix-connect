package deploy

import "strings"

func OutputPath(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if strings.HasPrefix(line, "/") && !strings.ContainsAny(line, " \t") {
			return line
		}
	}
	return firstLine(output)
}
