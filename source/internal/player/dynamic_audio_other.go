//go:build !windows

package player

func monitorAudio(a *App) {
	audioTick(a, func(DynamicAudioConfig) AudioLevels {
		return AudioLevels{Error: "Динамічна гучність доступна у Windows"}
	})
}
