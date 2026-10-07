package webapi

import (
	"github.com/vyto4ka/vynel/internal/decoy"
	"github.com/vyto4ka/vynel/internal/panel/service"
)

// settingDef describes a setting for the settings page.
type settingDef struct {
	Key, Section, Title, Help, Type, Default string
	Options                                  []string
}

var settingDefs = []settingDef{
	{Key: service.SettingSubDomain, Section: "Подписки", Title: "Домен подписок", Type: "string",
		Help: "Ссылки подписок строятся на этом домене. A-запись должна вести на сервер панели."},
	{Key: service.SettingSubPrefix, Section: "Подписки", Title: "Путь подписок", Type: "string", Default: service.DefaultSubPrefix,
		Help: "Начало пути ссылки, например /s/. После смены старые ссылки перестанут работать."},
	{Key: service.SettingSubDecoy, Section: "Подписки", Title: "Сайт-заглушка домена подписок", Type: "select", Default: "docs", Options: decoy.Names(),
		Help: "Что видит человек, открывший домен в браузере."},
	{Key: service.SettingSubPort, Section: "Подписки", Title: "Порт подписок", Type: "int", Default: "443"},

	{Key: service.SettingHWIDEnabled, Section: "Устройства (HWID)", Title: "Учитывать устройства", Type: "bool", Default: "true"},
	{Key: service.SettingHWIDLimit, Section: "Устройства (HWID)", Title: "Лимит устройств по умолчанию", Type: "int", Default: "3",
		Help: "Для пользователей без своего лимита. 0 — без ограничения."},
	{Key: service.SettingHWIDAllowNone, Section: "Устройства (HWID)", Title: "Пускать приложения без HWID", Type: "bool", Default: "false",
		Help: "Для всех пользователей. Для одного — переключатель «Проверять HWID» в его карточке. Иначе приложение без HWID получит сервер-заглушку с объяснением; Karing передаёт HWID, если включить X-HWID в настройках профиля."},

	{Key: service.SettingWebPath, Section: "Веб-панель", Title: "Секретный путь панели", Type: "string",
		Help: "Панель открывается только по этому пути, всё остальное — сайт-заглушка. После смены страница перезагрузится по новому адресу."},
	{Key: service.SettingWebDomain, Section: "Веб-панель", Title: "Отдельный домен панели", Type: "string",
		Help: "Пусто — панель на домене подписок. Для отдельного домена нужна A-запись на этот сервер."},

	{Key: service.SettingCaddyEmail, Section: "Сертификаты", Title: "Email для Let's Encrypt", Type: "string"},
	{Key: service.SettingCaddyIssuer, Section: "Сертификаты", Title: "Кто выдаёт сертификаты", Type: "select", Default: "acme", Options: []string{"acme", "internal"},
		Help: "acme — Let's Encrypt (нужен настоящий домен), internal — самоподписанные (для тестов)."},

	{Key: service.SettingGatewayAddr, Section: "Ноды", Title: "Адрес панели для нод", Type: "string",
		Help: "IP:порт, к которому подключаются дополнительные ноды. Попадает в токены подключения."},
	{Key: service.SettingXrayLog, Section: "Ноды", Title: "Логи Xray на нодах", Type: "select", Default: service.DefaultXrayLog,
		Options: []string{"none", "warning", "info", "debug"},
		Help:    "Смотреть на ноде: journalctl -u vynel-node -f. debug — для поиска проблем: пишет каждое подключение, после включите обратно warning."},
}

// hiddenSettings are internal and not listed (the login is changed on the account page).
var hiddenSettings = map[string]bool{
	service.SettingWebLogin: true, service.SettingGatewaySNI: true, service.SettingInstallCommand: true,
	// edited on the «Подписка» page
	service.SettingSubTitle: true, service.SettingSubUpdateHours: true, service.SettingSubSupportURL: true,
	service.SettingSubAnnounce: true, service.SettingSubAnnounceURL: true, service.SettingSubPageURL: true,
	service.SettingSubHeaders: true, service.SettingSubPage: true,
	// edited on the «Telegram» page
	service.SettingBotAdmins: true, service.SettingBotBackupTime: true, service.SettingBotTimezone: true,
	service.SettingBotAlerts: true, service.SettingBotAlertDelay: true, service.SettingBotAlertState: true, "bot.backup_last": true,
}
