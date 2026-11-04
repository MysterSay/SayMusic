# Say Music command API / API команд

## English

Start Say Music normally, then control the running player from a script. Requests use the authenticated local connection; no public network API is opened. `--api help` lists commands. Successful replies are JSON; failures return exit code 1, invalid arguments return 2. When launching the Windows GUI executable from a script, wait for the process if you need its result.

```powershell
.\SayMusic.exe --api status
.\SayMusic.exe --api play
.\SayMusic.exe --api pause
.\SayMusic.exe --api toggle
.\SayMusic.exe --api stop
.\SayMusic.exe --api next
.\SayMusic.exe --api previous
.\SayMusic.exe --api volume 65
.\SayMusic.exe --api seek 90
.\SayMusic.exe --api repeat one
.\SayMusic.exe --api repeat all
.\SayMusic.exe --api repeat off
.\SayMusic.exe --api playlist-create "Evening music"
.\SayMusic.exe --api playlists
.\SayMusic.exe --api playlist-add-current 4
.\SayMusic.exe --api dynamic on
.\SayMusic.exe --api dynamic off
```

Volume is 0–100; seek positions are seconds. `playlist-create` returns the new ID; `playlist-add-current` accepts a playlist ID. Playback commands are picked up within about 0.5 seconds.

### Storage

The selected cache folder contains `music/` (completed tracks), `data/` (database, settings, sessions, WebView2 profiles, logs, temporary buffers), and `tools/` (newly installed playback components). A small `storage-path.json` beside the EXE remembers the selected folder. Existing tools beside the EXE remain supported.

On upgrade, the player migrates legacy AppData storage and reorganizes cached tracks. Existing files with different contents are never overwritten. Changing the folder closes the player; start it again to finish migration. The database and browser profiles must be closed before moving. The executable folder needs write access for the path file.

### Dynamic volume

Enable it in Settings → Main. Select the output device to monitor; optionally enable microphone monitoring and choose an input. The player ignores its own process and WebView2 child processes. It monitors audio peak levels locally and does not save recordings. Volume fades to the minimum over one second; after the restore delay it fades back over one second. The maximum follows your regular volume slider; Current shows the actual volume. Windows microphone access may be required. When disabled, audio monitoring stops.

Seeking a track that is still downloading may wait for the full file before the requested position is available.

## Українська

Спочатку відкрий плеєр, потім запускай наведені вище команди. Гучність задається у відсотках 0–100, позиція — у секундах. `playlist-create` повертає ID нового плейлиста; `playlist-add-current` додає поточний трек у плейлист із зазначеним ID. Команди відтворення виконуються приблизно за 0,5 секунди. Для читання результату GUI-програми у скрипті дочекайся завершення процесу.

Дані зберігаються у вибраній папці: `music/` — завершені аудіофайли, `data/` — база, налаштування, сесії, профілі WebView2, журнали та буфери, `tools/` — компоненти, установлені новою версією. Поруч з EXE залишається лише `storage-path.json` зі шляхом до сховища. Раніше встановлені компоненти поруч із програмою також підтримуються.

Після оновлення старі дані AppData переносяться автоматично. При зміні папки плеєр закриється; запусти його знову для перенесення. Якщо є конфлікт файлів, оригінали зберігаються та показується помилка. Папка EXE має бути доступною для запису файла зі шляхом.

Динамічна гучність вмикається в «Налаштування → Головна». Вибери вихідний потік і, за потреби, мікрофон. Власний звук плеєра та його процесів WebView2 ігнорується. Аналізуються локальні рівні звуку, аудіозаписи не зберігаються. Приглушення триває секунду; після заданої затримки відновлення також триває секунду. Максимум відповідає звичайному повзунку, поле «Зараз» показує фактичну гучність. При вимкненій функції моніторинг припиняється. Для мікрофона може знадобитися дозвіл Windows.

Перемотування треку, який ще завантажується, може чекати на отримання повного файла.
