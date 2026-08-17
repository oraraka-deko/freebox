package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"context"

	"freebox/engine"
	"freebox/search"
)

func matchTypeFromCode(code C.FreeboxSearchMatchType) search.MatchType {
	switch code {
	case C.FREEBOX_SEARCH_MATCH_EXACT:
		return search.MatchExact
	case C.FREEBOX_SEARCH_MATCH_REGEX:
		return search.MatchRegex
	case C.FREEBOX_SEARCH_MATCH_GLOB:
		return search.MatchGlob
	case C.FREEBOX_SEARCH_MATCH_PREFIX:
		return search.MatchPrefix
	case C.FREEBOX_SEARCH_MATCH_SUFFIX:
		return search.MatchSuffix
	default:
		return search.MatchSubstring
	}
}

func targetFromCode(code C.FreeboxSearchTarget) search.SearchTarget {
	switch code {
	case C.FREEBOX_SEARCH_TARGET_NAMES:
		return search.TargetNameOnly
	case C.FREEBOX_SEARCH_TARGET_CONTENT:
		return search.TargetContentOnly
	default:
		return search.TargetNamesAndContent
	}
}

//export Freebox_Search
func Freebox_Search(
	engineHandle C.uint64_t,
	rootPathPtr *C.uint8_t, rootPathLen C.int32_t,
	namePatternPtr *C.uint8_t, namePatternLen C.int32_t,
	contentPatternPtr *C.uint8_t, contentPatternLen C.int32_t,
	replacementPtr *C.uint8_t, replacementLen C.int32_t,
	matchTypeCode C.FreeboxSearchMatchType,
	targetCode C.FreeboxSearchTarget,
	isReplace C.uint8_t,
	caseSensitive C.uint8_t,
	dartPort C.int64_t,
	outResult *C.FreeboxResultC,
) C.uint64_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil || inst.Engine == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	fullPath := cBytesToGoString(rootPathPtr, rootPathLen)
	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	namePattern := cBytesToGoString(namePatternPtr, namePatternLen)
	contentPattern := cBytesToGoString(contentPatternPtr, contentPatternLen)
	replacement := cBytesToGoString(replacementPtr, replacementLen)

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Search/Replace in " + fullPath,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				searchEngine := search.NewEngine(targetFS)
				mType := matchTypeFromCode(matchTypeCode)
				sTarget := targetFromCode(targetCode)

				query := search.SearchQuery{
					RootPath:         subPath,
					NamePattern:      namePattern,
					NameMatchType:    mType,
					ContentPattern:   contentPattern,
					ContentMatchType: mType,
					CaseSensitive:    caseSensitive != 0,
					Target:           sTarget,
					Replacement:      replacement,
					IsReplace:        isReplace != 0,
					OnProgress: func(stats search.SearchStats) {
						handle.UpdateProgress(stats.FilesScanned, 0, stats.CurrentPath)
					},
				}

				results, stats, err := searchEngine.Search(ctx, query)
				if err != nil {
					return err
				}

				handle.UpdateProgress(stats.FilesScanned, stats.FilesScanned, "Completed")
				handle.SetResult(len(results))
				return nil
			},
		},
	}

	handle, err := inst.Engine.Submit(t)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	uID := getTaskUintID(handle.ID())
	targetPort := int64(dartPort)
	if targetPort <= 0 {
		targetPort = inst.DartPort
	}
	setupTaskDartCallbacks(handle, uID, targetPort)

	if outResult != nil {
		outResult.success = 1
		outResult.handle = C.uint64_t(uID)
	}
	return C.uint64_t(uID)
}
