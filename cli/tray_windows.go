package cli

// tray_windows.go: ícone de bandeja do FastExec. Um clique abre a janela de métricas; o botão direito abre o menu
// (abrir, iniciar com o Windows, sair); a dica do ícone mostra os tokens economizados hoje, atualizada a cada 30 s.
// Só usa a API do Windows (user32/shell32/gdi32), sem dependência nova.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"homelab/fastexec/internal/telemetry"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassEx     = user32.NewProc("RegisterClassExW")
	pCreateWindowEx      = user32.NewProc("CreateWindowExW")
	pDefWindowProc       = user32.NewProc("DefWindowProcW")
	pGetMessage          = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessage     = user32.NewProc("DispatchMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pPostMessage         = user32.NewProc("PostMessageW")
	pSetTimer            = user32.NewProc("SetTimer")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenu          = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	pRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
	pShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	pCreateBitmap        = gdi32.NewProc("CreateBitmap")
	pGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	pCreateMutex         = kernel32.NewProc("CreateMutexW")
)

const (
	wmDestroy   = 0x0002
	wmCommand   = 0x0111
	wmTimer     = 0x0113
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmNull      = 0x0000
	wmTray      = 0x0400 + 1

	nimAdd, nimModify, nimDelete = 0, 1, 2
	nifMessage, nifIcon, nifTip  = 1, 2, 4

	mfString, mfChecked, mfSeparator = 0x0, 0x8, 0x800
	tpmBottomAlign, tpmRightButton   = 0x20, 0x2

	idOpen, idAutostart, idQuit = 1, 2, 3
	runKey                      = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue                    = "FastExecTray"
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type point struct{ X, Y int32 }

type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type iconInfo struct {
	FIcon    uint32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

type tray struct {
	hwnd        uintptr
	icon        uintptr
	mon         *monitorServer
	taskbarMsg  uint32
	lastTip     string
	lastTipSize int
}

var theTray *tray

// trayIcon desenha o ícone (círculo verde com um raio branco) direto em pixels.
func trayIcon() uintptr {
	const n = 32
	bolt := [][2]float64{{18, 3}, {7, 18}, {15, 18}, {13, 29}, {25, 13}, {17, 13}}
	inside := func(x, y float64) bool {
		in := false
		for i, j := 0, len(bolt)-1; i < len(bolt); j, i = i, i+1 {
			xi, yi, xj, yj := bolt[i][0], bolt[i][1], bolt[j][0], bolt[j][1]
			if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
				in = !in
			}
		}
		return in
	}
	pix := make([]byte, n*n*4) // BGRA
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			dx, dy := fx-16, fy-16
			o := (y*n + x) * 4
			if dx*dx+dy*dy > 15.5*15.5 {
				continue // transparente
			}
			if inside(fx, fy) {
				pix[o], pix[o+1], pix[o+2], pix[o+3] = 255, 255, 255, 255
			} else {
				pix[o], pix[o+1], pix[o+2], pix[o+3] = 0x5F, 0x7B, 0x0F, 255 // #0F7B5F
			}
		}
	}
	mask := make([]byte, 4*n) // 1 bpp, 32 linhas de 4 bytes (alinhadas em 16 bits); tudo 0 = ícone opaco onde o alfa vale
	color, _, _ := pCreateBitmap.Call(n, n, 1, 32, uintptr(unsafe.Pointer(&pix[0])))
	mk, _, _ := pCreateBitmap.Call(n, n, 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	ii := iconInfo{FIcon: 1, HbmMask: mk, HbmColor: color}
	h, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	return h
}

func (t *tray) notify(msg uintptr, tip string) bool {
	nd := notifyIconData{HWnd: t.hwnd, UID: 1, UFlags: nifMessage | nifIcon | nifTip, UCallbackMessage: wmTray, HIcon: t.icon}
	nd.CbSize = uint32(unsafe.Sizeof(nd))
	tp, _ := windows.UTF16FromString(tip)
	if len(tp) > len(nd.SzTip) {
		tp = tp[:len(nd.SzTip)-1]
		tp = append(tp, 0)
	}
	copy(nd.SzTip[:], tp)
	r, _, _ := pShellNotifyIcon.Call(msg, uintptr(unsafe.Pointer(&nd)))
	return r != 0
}

func (t *tray) tooltip() string {
	rep, err := telemetry.BuildSavingsReport("today")
	if err != nil {
		return "FastExec"
	}
	return fmt.Sprintf("FastExec: %s tokens saved today (%.0f%%)", groupDigits(rep.SavedTokens), rep.Percent)
}

func groupDigits(n int64) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (t *tray) openWindow() {
	if t.mon == nil {
		m, err := newMonitorServer()
		if err != nil {
			return
		}
		t.mon = m
	}
	if t.mon.clients.Load() > 0 {
		return // já aberta
	}
	openBrowserSize(t.mon.url(), 1080, 900)
}

func autostartOn() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func setAutostart(on bool) {
	exe, _ := os.Executable()
	setAutostartFor(exe, on)
}

func setAutostartFor(exe string, on bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if !on {
		_ = k.DeleteValue(runValue)
		return
	}
	if exe != "" {
		_ = k.SetStringValue(runValue, `"`+exe+`" tray`)
	}
}

// startTray é chamado pela instalação: liga "iniciar com o Windows" e abre o ícone de bandeja agora (sem console).
// Uma segunda instância sai sozinha (mutex), então repetir é seguro. Devolve o que foi feito, para o log.
func startTray(exe string, dry bool) string {
	exe = guiTwin(exe)
	if dry {
		return "would start the tray icon and add it to Windows startup"
	}
	if os.Getenv("FASTEXEC_NO_TRAY") != "" {
		return ""
	}
	if !autostartOn() {
		setAutostartFor(exe, true)
	}
	cmd := exec.Command(exe, "tray")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000 | 0x00000008}
	if err := cmd.Start(); err != nil {
		return "could not start the tray icon: " + err.Error()
	}
	_ = cmd.Process.Release()
	return "tray icon started (and set to start with Windows)"
}


func (t *tray) menu() {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(flags, id uintptr, text string) {
		p, _ := windows.UTF16PtrFromString(text)
		pAppendMenu.Call(m, flags, id, uintptr(unsafe.Pointer(p)))
	}
	add(mfString, idOpen, "Open savings window")
	flags := uintptr(mfString)
	if autostartOn() {
		flags |= mfChecked
	}
	add(flags, idAutostart, "Start with Windows")
	add(mfSeparator, 0, "")
	add(mfString, idQuit, "Quit")
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(t.hwnd)
	pTrackPopupMenu.Call(m, tpmBottomAlign|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	pPostMessage.Call(t.hwnd, wmNull, 0, 0)
}

func trayProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	t := theTray
	if t == nil {
		r, _, _ := pDefWindowProc.Call(hwnd, msg, wparam, lparam)
		return r
	}
	switch uint32(msg) {
	case wmTray:
		switch uint32(lparam) & 0xFFFF {
		case wmLButtonUp:
			t.openWindow()
		case wmRButtonUp:
			t.menu()
		}
		return 0
	case wmCommand:
		switch wparam & 0xFFFF {
		case idOpen:
			t.openWindow()
		case idAutostart:
			setAutostart(!autostartOn())
		case idQuit:
			t.notify(nimDelete, "")
			pPostQuitMessage.Call(0)
		}
		return 0
	case wmTimer:
		t.notify(nimModify, t.tooltip())
		return 0
	case wmDestroy:
		t.notify(nimDelete, "")
		pPostQuitMessage.Call(0)
		return 0
	}
	if t.taskbarMsg != 0 && uint32(msg) == t.taskbarMsg { // o Explorer reiniciou: recoloca o ícone
		t.notify(nimAdd, t.tooltip())
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

const trayUsage = `Usage: fastexec tray
  Puts a FastExec icon in the notification area. Click it to open the savings window; right-click for the menu.`

// cmdTray roda o ícone até "Quit". Uma só instância por usuário.
func cmdTray(args []string) {
	if len(args) > 0 {
		fmt.Println(trayUsage)
		os.Exit(2)
	}
	runtime.LockOSThread()
	name, _ := windows.UTF16PtrFromString(`Local\FastExecTray`)
	h, _, err := pCreateMutex.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if h == 0 || err == syscall.ERROR_ALREADY_EXISTS {
		return // já existe um ícone
	}

	inst, _, _ := pGetModuleHandle.Call(0)
	cls, _ := windows.UTF16PtrFromString("FastExecTrayWnd")
	wc := wndClassEx{Instance: inst, WndProc: syscall.NewCallback(trayProc), ClassName: cls}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, e := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		fmt.Fprintln(os.Stderr, "tray: cannot register the window class:", e)
		os.Exit(1)
	}
	t := &tray{}
	theTray = t
	hw, _, e := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	if hw == 0 {
		fmt.Fprintln(os.Stderr, "tray: cannot create the window:", e)
		os.Exit(1)
	}
	t.hwnd = hw
	t.icon = trayIcon()
	tb, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := pRegisterWindowMsg.Call(uintptr(unsafe.Pointer(tb)))
	t.taskbarMsg = uint32(r)
	if !t.notify(nimAdd, t.tooltip()) {
		fmt.Fprintln(os.Stderr, "tray: the notification area refused the icon")
		os.Exit(1)
	}
	pSetTimer.Call(hw, 1, 30000, 0)

	var m winMsg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	if t.mon != nil {
		t.mon.close()
	}
}
