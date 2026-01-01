package mcserver

import (
	"regexp"
)

type failurePattern struct {
	name    string
	re      *regexp.Regexp
	message string
}

var builtinFailurePatterns = []failurePattern{
	{
		name:    "jvm_insufficient_memory",
		re:      regexp.MustCompile(`There is insufficient memory for the Java Runtime Environment to continue`),
		message: "Java 런타임에 할당할 메모리가 부족합니다. 서버 메모리 설정을 확인해주세요.",
	},
	{
		name:    "jvm_native_memory_allocation_failed",
		re:      regexp.MustCompile(`Native memory allocation \(.*\) failed`),
		message: "네이티브 메모리 할당에 실패했습니다. 시스템 메모리가 부족합니다.",
	},
	{
		name:    "mc_server_runner_failed",
		re:      regexp.MustCompile(`mc-server-runner.*Minecraft server failed`),
		message: "Minecraft 서버 실행에 실패했습니다. 서버 로그를 확인해주세요.",
	},
	{
		name:    "java_error_report",
		re:      regexp.MustCompile(`# An error report file with more information is saved as:`),
		message: "Java 오류가 발생했습니다. 오류 보고서 파일을 확인해주세요.",
	},
	{
		name:    "port_already_in_use",
		re:      regexp.MustCompile(`Failed to bind to port`),
		message: "서버 포트가 이미 사용 중입니다.",
	},
	{
		name:    "eula_not_accepted",
		re:      regexp.MustCompile(`You need to agree to the EULA`),
		message: "EULA에 동의해야 합니다. eula.txt 파일을 확인해주세요.",
	},
}

type FailureMatch struct {
	PatternName string
	Message     string
}

func checkFailurePatterns(logText string) *FailureMatch {
	for _, pattern := range builtinFailurePatterns {
		if pattern.re.MatchString(logText) {
			return &FailureMatch{
				PatternName: pattern.name,
				Message:     pattern.message,
			}
		}
	}
	return nil
}

var shutdownPatterns = []*regexp.Regexp{
	regexp.MustCompile(`Stopping server`),
	regexp.MustCompile(`Stopping the server`),
	regexp.MustCompile(`\[Server thread/INFO].*Saving worlds`),
	regexp.MustCompile(`Server closed`),
	regexp.MustCompile(`ThreadedAnvilChunkStorage.*All dimensions are saved`),
}

func isShutdownLog(logText string) bool {
	for _, pattern := range shutdownPatterns {
		if pattern.MatchString(logText) {
			return true
		}
	}
	return false
}
