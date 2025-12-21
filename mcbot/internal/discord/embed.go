package discord

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	ColorSuccess = 0x00FF00
	ColorError   = 0xFF0000
	ColorInfo    = 0x0099FF
	ColorWarning = 0xFFCC00
)

func EmbedStarting() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🚀 서버 시작 중",
		Description: "마인크래프트 서버를 시작하고 있습니다...\n로딩이 완료되면 알려드릴게요.",
		Color:       ColorInfo,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func EmbedStartSuccess(loadSeconds float64, requestedBy string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "✅ 서버 시작 완료",
		Description: "마인크래프트 서버가 정상적으로 시작되었습니다!",
		Color:       ColorSuccess,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "상태", Value: "🟢 온라인", Inline: true},
			{Name: "로드 시간", Value: fmt.Sprintf("%.2f초", loadSeconds), Inline: true},
			{Name: "요청자", Value: requestedBy, Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

func EmbedStartFailed(errorMsg string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "❌ 서버 시작 실패",
		Description: errorMsg,
		Color:       ColorError,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func EmbedStopping() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🛑 서버 종료 중",
		Description: "마인크래프트 서버를 정상 종료하고 있습니다...\n월드 데이터를 저장 중이니 잠시만 기다려주세요.",
		Color:       ColorWarning,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func EmbedStopSuccess(requestedBy string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "✅ 서버 종료 완료",
		Description: "마인크래프트 서버가 정상적으로 종료되었습니다.",
		Color:       ColorSuccess,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "상태", Value: "🔴 오프라인", Inline: true},
			{Name: "요청자", Value: requestedBy, Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

func EmbedStopFailed(errorMsg string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "❌ 서버 종료 실패",
		Description: errorMsg,
		Color:       ColorError,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func EmbedStatus(stateKorean string, isOnline bool, lastStartTime time.Time, lastReadyDuration time.Duration) *discordgo.MessageEmbed {
	statusIcon := "🔴"
	statusText := "오프라인"
	if isOnline {
		statusIcon = "🟢"
		statusText = "온라인"
	}

	embed := &discordgo.MessageEmbed{
		Title: "📊 서버 상태",
		Color: ColorInfo,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "상태", Value: fmt.Sprintf("%s %s", statusIcon, statusText), Inline: true},
			{Name: "내부 상태", Value: stateKorean, Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if isOnline && !lastStartTime.IsZero() {
		uptime := time.Since(lastStartTime)
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "가동 시간",
			Value:  formatDuration(uptime),
			Inline: true,
		})
	}

	if lastReadyDuration > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "마지막 로드 시간",
			Value:  fmt.Sprintf("%.2f초", lastReadyDuration.Seconds()),
			Inline: true,
		})
	}

	return embed
}

func EmbedError(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       ColorError,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func EmbedPermissionDenied(requiredRole string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🚫 권한 없음",
		Description: fmt.Sprintf("이 명령어를 사용하려면 `%s` 역할이 필요합니다.", requiredRole),
		Color:       ColorError,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%d시간 %d분 %d초", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%d분 %d초", m, s)
	}
	return fmt.Sprintf("%d초", s)
}
