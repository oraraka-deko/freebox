package main

/*
#include "include/freebox_bridge.h"
*/
import "C"

//export Freebox_TelegramListSessions
func Freebox_TelegramListSessions(
	engineHandle C.uint64_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.TelegramMgr == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("telegram manager not initialized")
		}
		return 0
	}

	accounts, err := inst.TelegramMgr.ListAccounts()
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	if outResult != nil {
		outResult.success = 1
		outResult.value = C.int64_t(len(accounts))
	}
	return 1
}

//export Freebox_TelegramRemoveSession
func Freebox_TelegramRemoveSession(
	engineHandle C.uint64_t,
	accountID C.int64_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.TelegramMgr == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("telegram manager not initialized")
		}
		return 0
	}

	if err := inst.TelegramMgr.DeleteAccount(int64(accountID)); err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}
