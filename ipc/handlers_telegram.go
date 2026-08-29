package ipc

import "encoding/json"

func init() {
	register("telegram.listSessions", handleTelegramListSessions)
	register("telegram.removeSession", handleTelegramRemoveSession)
}

func handleTelegramListSessions(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	if inst.TelegramMgr == nil {
		return nil, errTelegramNotInitialized
	}
	accounts, err := inst.TelegramMgr.ListAccounts()
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

type telegramRemoveSessionParams struct {
	AccountID int64 `json:"accountId"`
}

func handleTelegramRemoveSession(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	if inst.TelegramMgr == nil {
		return nil, errTelegramNotInitialized
	}
	var p telegramRemoveSessionParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if err := inst.TelegramMgr.DeleteAccount(p.AccountID); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
