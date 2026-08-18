import { app, BrowserWindow, Menu, type MenuItemConstructorOptions } from 'electron'
import { createHomeWindow, createMonitorWindow, focusAdjacentWindow } from './windows'

// v10 Torch (task 0148): CmdOrCtrl+, opens Settings for whichever window is
// focused. The renderer decides what "Settings" means for its scope — a
// project window opens its project Settings surface (even in chat-focus),
// the home window opens global app settings. With no focused window, surface
// the home window first so the shortcut never lands nowhere.
function openSettingsInFocusedWindow(): void {
  const focused = BrowserWindow.getFocusedWindow()
  if (focused) {
    focused.webContents.send('open-settings')
    return
  }
  // No focused window: surface the home window, deferring the message until
  // its renderer is ready if it was just created.
  const win = createHomeWindow()
  if (win.webContents.isLoading()) {
    win.webContents.once('did-finish-load', () => win.webContents.send('open-settings'))
  } else {
    win.webContents.send('open-settings')
  }
}

// Build and install the application menu (v8 Inferno — Feature 1).
//
// Before multi-window, the GUI had no app Menu — a single window needed no
// File/Window menu and the renderer owned all navigation. With independent
// per-project windows we want discoverable, OS-native shortcuts:
//   - Cmd/Ctrl+N        → open (or focus) the home / dashboard window, the
//                         surface where you pick which project to open.
//   - Cmd/Ctrl+Shift+M  → open (or focus) the always-on-top mini-monitor.
//   - Cmd/Ctrl+Shift+]  → focus the next window in the registry.
//   - Cmd/Ctrl+Shift+[  → focus the previous window.
// The rest of the template is the standard Electron menu so copy/paste,
// zoom, reload, fullscreen, and the macOS app menu keep working once we stop
// relying on the default (which `Menu.setApplicationMenu` replaces).
export function buildAppMenu(): void {
  const isMac = process.platform === 'darwin'

  const macAppMenu: MenuItemConstructorOptions[] = isMac
    ? [
        {
          label: app.name,
          submenu: [
            { role: 'about' },
            { type: 'separator' },
            {
              label: 'Settings…',
              accelerator: 'CmdOrCtrl+,',
              click: () => openSettingsInFocusedWindow()
            },
            { type: 'separator' },
            { role: 'services' },
            { type: 'separator' },
            { role: 'hide' },
            { role: 'hideOthers' },
            { role: 'unhide' },
            { type: 'separator' },
            { role: 'quit' }
          ]
        }
      ]
    : []

  const template: MenuItemConstructorOptions[] = [
    ...macAppMenu,
    {
      label: 'File',
      submenu: [
        {
          label: 'New Window',
          accelerator: 'CmdOrCtrl+N',
          click: () => createHomeWindow()
        },
        // Windows/Linux keep Settings under File (macOS puts it in the app
        // menu above, per platform convention).
        ...(!isMac
          ? ([
              { type: 'separator' },
              {
                label: 'Settings…',
                accelerator: 'CmdOrCtrl+,',
                click: () => openSettingsInFocusedWindow()
              }
            ] as MenuItemConstructorOptions[])
          : []),
        { type: 'separator' },
        isMac ? { role: 'close' } : { role: 'quit' }
      ]
    },
    {
      label: 'Edit',
      submenu: [
        { role: 'undo' },
        { role: 'redo' },
        { type: 'separator' },
        { role: 'cut' },
        { role: 'copy' },
        { role: 'paste' },
        { role: 'selectAll' }
      ]
    },
    {
      label: 'View',
      submenu: [
        { role: 'reload' },
        { role: 'forceReload' },
        { role: 'toggleDevTools' },
        { type: 'separator' },
        { role: 'resetZoom' },
        { role: 'zoomIn' },
        { role: 'zoomOut' },
        { type: 'separator' },
        { role: 'togglefullscreen' }
      ]
    },
    {
      label: 'Window',
      submenu: [
        { role: 'minimize' },
        { role: 'zoom' },
        { type: 'separator' },
        {
          label: 'Mini Monitor',
          accelerator: 'CmdOrCtrl+Shift+M',
          click: () => createMonitorWindow()
        },
        { type: 'separator' },
        {
          label: 'Next Window',
          accelerator: 'CmdOrCtrl+Shift+]',
          click: () => focusAdjacentWindow(1)
        },
        {
          label: 'Previous Window',
          accelerator: 'CmdOrCtrl+Shift+[',
          click: () => focusAdjacentWindow(-1)
        },
        ...(isMac
          ? ([{ type: 'separator' }, { role: 'front' }] as MenuItemConstructorOptions[])
          : ([{ role: 'close' }] as MenuItemConstructorOptions[]))
      ]
    }
  ]

  Menu.setApplicationMenu(Menu.buildFromTemplate(template))
}
