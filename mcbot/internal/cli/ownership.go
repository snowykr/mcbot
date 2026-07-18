package cli

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/snowy/mcbot/internal/mcconfig"
)

type ownershipPair struct {
	UID int
	GID int
}

type ownershipCandidate struct {
	label       string
	pair        ownershipPair
	description string
	recommended bool
	custom      bool
}

type ownershipDetection struct {
	currentUser *ownershipPair
	dataDir     *ownershipPair
	fallback    ownershipPair
}

var detectCurrentOwnership = defaultDetectCurrentOwnership
var detectPathOwnership = defaultDetectPathOwnership

func setOwnershipDetectorsForTest(current func() (ownershipPair, bool), path func(string) (ownershipPair, bool, error)) func() {
	oldCurrent := detectCurrentOwnership
	oldPath := detectPathOwnership
	detectCurrentOwnership = current
	detectPathOwnership = path
	return func() {
		detectCurrentOwnership = oldCurrent
		detectPathOwnership = oldPath
	}
}

func detectSetupOwnership(configPath string) (ownershipDetection, error) {
	current, hasCurrent := detectCurrentOwnership()
	dataPath := filepath.Join(filepath.Dir(configPath), "data")
	data, hasData, err := detectPathOwnership(dataPath)
	if err != nil {
		return ownershipDetection{}, fmt.Errorf("inspect %s ownership: %w", dataPath, err)
	}
	detection := ownershipDetection{fallback: ownershipPair{UID: 1000, GID: 1000}}
	if hasCurrent {
		detection.currentUser = &current
	}
	if hasData {
		detection.dataDir = &data
	}
	return detection, nil
}

func promptContainerOwnership(cfg *mcconfig.Config, prompter Prompter, opts Options, out Output, path string, existingConfig bool) error {
	detection, err := detectSetupOwnership(path)
	if err != nil {
		return err
	}
	if opts.Yes || opts.NoInput {
		applyAutomaticOwnership(cfg, detection, existingConfig)
		return nil
	}

	candidates := ownershipCandidates(cfg, detection, existingConfig)

	if err := out.SetupQuestion(
		"Container behavior · advanced",
		"Container file ownership",
		"Choose the host UID/GID that should own Minecraft files under ./data.",
		"Use 1001:1001 only for legacy/cloud volumes that already use it.",
	); err != nil {
		return err
	}

	options := make([]string, 0, len(candidates))
	defaultIndex := 0
	for i, candidate := range candidates {
		label := fmt.Sprintf("%s — %d:%d", candidate.label, candidate.pair.UID, candidate.pair.GID)
		if candidate.custom {
			label = candidate.label
		}
		if candidate.description != "" {
			label += " — " + candidate.description
		}
		if candidate.recommended {
			label += " (recommended)"
			defaultIndex = i
		}
		options = append(options, label)
	}

	selected, err := prompter.Select("Choose", options, defaultIndex)
	if err != nil {
		return err
	}
	selectedIndex := indexOf(options, selected)
	if selectedIndex < 0 {
		selectedIndex = defaultIndex
	}
	choice := candidates[selectedIndex]
	if !choice.custom {
		cfg.Container.UID = choice.pair.UID
		cfg.Container.GID = choice.pair.GID
		return nil
	}

	uid, err := promptOwnershipInt(prompter, out, "Container UID (container.uid)", cfg.Container.UID)
	if err != nil {
		return err
	}
	gid, err := promptOwnershipInt(prompter, out, "Container GID (container.gid)", cfg.Container.GID)
	if err != nil {
		return err
	}
	cfg.Container.UID = uid
	cfg.Container.GID = gid
	return nil
}

func applyAutomaticOwnership(cfg *mcconfig.Config, detection ownershipDetection, existingConfig bool) {
	if existingConfig {
		return
	}
	if detection.dataDir != nil && detection.dataDir.UID != 0 {
		cfg.Container.UID = detection.dataDir.UID
		cfg.Container.GID = detection.dataDir.GID
		return
	}
	if detection.currentUser != nil && detection.currentUser.UID != 0 {
		cfg.Container.UID = detection.currentUser.UID
		cfg.Container.GID = detection.currentUser.GID
		return
	}
	cfg.Container.UID = detection.fallback.UID
	cfg.Container.GID = detection.fallback.GID
}

func applyDefaultSetupOwnership(cfg *mcconfig.Config, path string, existingConfig bool) error {
	if existingConfig {
		return nil
	}
	detection, err := detectSetupOwnership(path)
	if err != nil {
		return err
	}
	applyAutomaticOwnership(cfg, detection, existingConfig)
	return nil
}

func ownershipCandidates(cfg *mcconfig.Config, detection ownershipDetection, existingConfig bool) []ownershipCandidate {
	candidates := make([]ownershipCandidate, 0, 5)
	add := func(candidate ownershipCandidate) {
		for _, existing := range candidates {
			if existing.pair == candidate.pair && !candidate.custom {
				return
			}
		}
		candidates = append(candidates, candidate)
	}

	if existingConfig {
		add(ownershipCandidate{
			label:       "Keep current config",
			pair:        ownershipPair{UID: cfg.Container.UID, GID: cfg.Container.GID},
			description: "preserves the UID/GID already in mc-server.toml",
			recommended: true,
		})
	}
	if detection.dataDir != nil {
		add(ownershipCandidate{
			label:       "Use existing ./data owner",
			pair:        *detection.dataDir,
			description: "best when reusing existing Minecraft data",
			recommended: !existingConfig && detection.dataDir.UID != 0,
		})
	}
	if detection.currentUser != nil {
		add(ownershipCandidate{
			label:       "Use current user",
			pair:        *detection.currentUser,
			description: "matches the account running setup",
			recommended: !existingConfig && (detection.dataDir == nil || detection.dataDir.UID == 0) && detection.currentUser.UID != 0,
		})
	}
	add(ownershipCandidate{
		label:       "Use standard Linux default",
		pair:        detection.fallback,
		description: "common first user on Linux hosts",
		recommended: !existingConfig && (detection.dataDir == nil || detection.dataDir.UID == 0) && (detection.currentUser == nil || detection.currentUser.UID == 0),
	})
	add(ownershipCandidate{
		label:       "Custom UID/GID",
		pair:        ownershipPair{UID: cfg.Container.UID, GID: cfg.Container.GID},
		description: "enter another numeric pair, such as 1001:1001 for existing legacy/cloud volumes",
		custom:      true,
	})
	return candidates
}

func promptOwnershipInt(prompter Prompter, out Output, label string, current int) (int, error) {
	for {
		if err := out.SetupQuestion("Container behavior · advanced", label, "Controls ownership of files under ./data."); err != nil {
			return 0, err
		}
		answer, err := prompter.Input("Value", fmt.Sprint(current))
		if err != nil {
			return 0, err
		}
		value, err := strconv.Atoi(answer)
		if err == nil && value >= 0 {
			return value, nil
		}
		if err := out.SetupHint("Please enter a non-negative integer."); err != nil {
			return 0, err
		}
	}
}
