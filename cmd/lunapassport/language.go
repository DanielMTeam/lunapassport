package main

import (
	"strconv"
	"strings"
)

type wizardCopy struct {
	PageTitle          string
	BrandSubtitle      string
	VerifyTitle        string
	ReadyTitle         string
	AddressTitle       string
	LoginLabel         string
	PasswordLabel      string
	NativeMessage      string
	NativeSignIn       string
	NativeNext         string
	NativeUnavailable  string
	PassportRequired   string
	FallbackHint       string
	InvalidCredentials string
	ReadyMessage       string
	FinishMessage      string
	AddressPrompt      string
	AddressInfo        string
}

var wizardCopies = map[string]wizardCopy{
	"en": {
		PageTitle:          "LunaPassport",
		BrandSubtitle:      "Connect through .NET Passport",
		VerifyTitle:        "Verify your .NET Passport",
		ReadyTitle:         "Your .NET Passport is ready",
		AddressTitle:       "Enter your .NET Passport address",
		LoginLabel:         "Sign-in name:",
		PasswordLabel:      "Password:",
		NativeMessage:      "Windows XP will open the sign-in window.",
		NativeSignIn:       "Sign in to LunaPassport to link your account with Windows XP and let supported applications use it.",
		NativeNext:         "Click Next to open the sign-in window.",
		NativeUnavailable:  "Something went wrong. Go back and try again.",
		PassportRequired:   "You must sign in through .NET Passport before continuing.",
		FallbackHint:       "If the native dialog does not appear, use the local LunaPassport form.",
		InvalidCredentials: "The e-mail address or password is incorrect.",
		ReadyMessage:       "The account passed the verification.",
		FinishMessage:      "Click Finish to complete the Wizard.",
		AddressPrompt:      "Passport sign-in name:",
		AddressInfo:        "Enter the account address to connect to Windows XP.",
	},
	"ru": {
		PageTitle:          "LunaPassport",
		BrandSubtitle:      "Привязка через .NET Passport",
		VerifyTitle:        "Проверка .NET Passport",
		ReadyTitle:         "Ваш .NET Passport готов",
		AddressTitle:       "Введите адрес .NET Passport",
		LoginLabel:         "Логин:",
		PasswordLabel:      "Пароль:",
		NativeMessage:      "Windows XP откроет окно входа.",
		NativeSignIn:       "Войдите в LunaPassport, чтобы привязать аккаунт к Windows XP и разрешить его использование в поддерживаемых приложениях.",
		NativeNext:         "Нажмите «Далее», чтобы открыть окно входа.",
		NativeUnavailable:  "Что-то пошло не так. Вернитесь назад и попробуйте снова.",
		PassportRequired:   "Для продолжения необходимо войти через .NET Passport.",
		FallbackHint:       "Если нативное окно не появилось, используйте локальную форму LunaPassport.",
		InvalidCredentials: "Неверный адрес электронной почты или пароль.",
		ReadyMessage:       "Аккаунт прошёл проверку.",
		FinishMessage:      "Нажмите Finish, чтобы завершить работу мастера.",
		AddressPrompt:      "Логин .NET Passport:",
		AddressInfo:        "Введите адрес аккаунта, который нужно привязать к Windows XP.",
	},
}

func detectWizardLanguage(lcid, langid, acceptLanguage string) string {
	for _, raw := range []string{lcid, langid} {
		if language := languageFromLCID(raw); language != "" {
			return language
		}
	}
	for _, item := range strings.Split(acceptLanguage, ",") {
		item = strings.TrimSpace(strings.SplitN(item, ";", 2)[0])
		if language := normalizeLanguage(item); language != "" {
			return language
		}
	}
	return "en"
}

func languageFromLCID(raw string) string {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	switch value {
	case 1049, 2073, 1091, 3098:
		return "ru"
	case 1033, 2057, 3081, 4105, 5129, 6153, 7177, 8201, 9225, 10249, 11273, 12297:
		return "en"
	default:
		return ""
	}
}

func normalizeLanguage(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(raw, "ru") {
		return "ru"
	}
	if strings.HasPrefix(raw, "en") {
		return "en"
	}
	return ""
}

func copyForLanguage(language string) wizardCopy {
	if copy, ok := wizardCopies[language]; ok {
		return copy
	}
	return wizardCopies["en"]
}
