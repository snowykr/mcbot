package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

const (
	ColorSuccess = 0x00FF00
	ColorError   = 0xFF0000
	ColorWarning = 0xFFCC00

	ComponentIDToggle            = "mcserver_toggle"
	ComponentIDConfirmStopPrefix = "mcserver_confirm_stop:"
	ComponentIDCancelStopPrefix  = "mcserver_cancel_stop:"
	EmbedMarkerFooter            = "MCBOT"

	maxVisiblePlayers = 10
)

func EmbedStartSuccess(readyDuration time.Duration, requestedBy string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "✅ 서버 시작 완료",
		Description: "마인크래프트 서버가 정상적으로 시작되었습니다!",
		Color:       ColorSuccess,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "상태", Value: "🟢 온라인", Inline: true},
			{Name: "준비 시간", Value: fmt.Sprintf("%.2f초", readyDuration.Seconds()), Inline: true},
			{Name: "요청자", Value: EscapeDiscordText(requestedBy), Inline: true},
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

func EmbedStopSuccess(requestedBy string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "✅ 서버 종료 완료",
		Description: "마인크래프트 서버가 정상적으로 종료되었습니다.",
		Color:       ColorSuccess,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "상태", Value: "🔴 오프라인", Inline: true},
			{Name: "요청자", Value: EscapeDiscordText(requestedBy), Inline: true},
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

func EmbedPermissionDenied(requiredRole string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🚫 권한 없음",
		Description: fmt.Sprintf("이 명령어를 사용하려면 `%s` 역할이 필요합니다.", requiredRole),
		Color:       ColorError,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func EmbedPersistentStatus(p mcserver.PresenceState) *discordgo.MessageEmbed {
	var statusIcon, statusText string
	var color int

	switch p.ServerState {
	case state.StateRunning:
		statusIcon = "🟢"
		statusText = "열림"
		color = ColorSuccess
	case state.StateStarting:
		statusIcon = "🟡"
		statusText = "여는중"
		color = ColorWarning
	case state.StateStopping:
		statusIcon = "🟡"
		statusText = "닫는중"
		color = ColorWarning
	case state.StateCrashed:
		statusIcon = "💥"
		statusText = "닫힘(크래시)"
		color = ColorError
	default:
		statusIcon = "🔴"
		statusText = "닫힘"
		color = ColorError
	}

	playersText := formatPlayers(p.Players)

	embed := &discordgo.MessageEmbed{
		Title:       "🎮 마인크래프트 서버 상태",
		Description: fmt.Sprintf("%s **%s**", statusIcon, statusText),
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "접속 현황",
				Value: playersText,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: EmbedMarkerFooter,
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	return embed
}

func formatPlayers(players []string) string {
	total := len(players)

	if total == 0 {
		return "현재 접속자(0명)\n- 없음"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("현재 접속자(%d명)\n", total))

	visibleCount := total
	if visibleCount > maxVisiblePlayers {
		visibleCount = maxVisiblePlayers
	}

	for i := 0; i < visibleCount; i++ {
		sb.WriteString("- ")
		sb.WriteString(EscapeDiscordText(players[i]))
		sb.WriteString("\n")
	}

	hiddenCount := total - visibleCount
	if hiddenCount > 0 {
		sb.WriteString(fmt.Sprintf("_...외 %d명_", hiddenCount))
	} else {
		result := sb.String()
		return strings.TrimSuffix(result, "\n")
	}

	return sb.String()
}

func BuildToggleButton(p mcserver.PresenceState) discordgo.Button {
	var label string
	var style discordgo.ButtonStyle
	var disabled bool

	switch p.ServerState {
	case state.StateRunning:
		label = "서버 닫기"
		style = discordgo.DangerButton
		disabled = false
	case state.StateStarting:
		label = "서버 여는중"
		style = discordgo.SecondaryButton
		disabled = true
	case state.StateStopping:
		label = "서버 닫는중"
		style = discordgo.SecondaryButton
		disabled = true
	case state.StateCrashed:
		label = "서버 재시작"
		style = discordgo.SuccessButton
		disabled = false
	default:
		label = "서버 열기"
		style = discordgo.SuccessButton
		disabled = false
	}

	return discordgo.Button{
		Label:    label,
		Style:    style,
		CustomID: ComponentIDToggle,
		Disabled: disabled,
	}
}
