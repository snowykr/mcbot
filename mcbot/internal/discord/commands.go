package discord

import "github.com/bwmarrin/discordgo"

const (
	CommandName = "마크봇"

	ActionStart  = "켜기"
	ActionStop   = "끄기"
	ActionStatus = "상태"
)

var Commands = []*discordgo.ApplicationCommand{
	{
		Name:        CommandName,
		Description: "마인크래프트 서버를 제어합니다",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "action",
				Description: "수행할 작업을 선택하세요",
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: ActionStart, Value: ActionStart},
					{Name: ActionStop, Value: ActionStop},
					{Name: ActionStatus, Value: ActionStatus},
				},
			},
		},
	},
}
