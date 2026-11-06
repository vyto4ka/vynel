package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

// SettingBotNode is zero for direct requests, or the remote node used only for the bot's API.
const SettingBotNode = "bot.node_id"

func (s *Service) BotNodeID(ctx context.Context) (int64, error) {
	raw, err := s.Setting(ctx, SettingBotNode, "0")
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0, invalid("неверная нода для Telegram: выберите её заново")
	}
	return id, nil
}

// ValidateBotNode allows an offline node to be selected, but never the panel's local node or
// an agent that would silently ignore the relay request. Runtime dials check live capabilities.
func (s *Service) ValidateBotNode(ctx context.Context, id int64) error {
	if id == 0 {
		return nil
	}
	if id < 0 {
		return invalid("неверная нода для Telegram: выберите её заново")
	}
	n, err := s.Node(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return invalid("нода для Telegram не найдена: выберите другую")
	}
	if err != nil {
		return err
	}
	if n.Local {
		return invalid("для Telegram выберите удалённую ноду, а не сервер панели")
	}
	if !n.Enabled {
		return invalid("нода для Telegram выключена: включите её или выберите другую")
	}
	if !n.Can(telegramrelay.Capability) {
		return invalid("сначала обновите агент выбранной ноды: он ещё не поддерживает Telegram через ноду")
	}
	return nil
}

func (s *Service) SetBotNode(ctx context.Context, actor Actor, id int64) error {
	if err := s.ValidateBotNode(ctx, id); err != nil {
		return err
	}
	return s.SetSetting(ctx, actor, SettingBotNode, strconv.FormatInt(id, 10))
}
