//go:build qa

package main

import "os"

// browserArgs exposes the WebView2 DevTools port for test/qa_gui.py. Only QA
// builds (wails3 build EXTRA_TAGS=qa) contain this; release builds never do.
func browserArgs() []string {
	if p := os.Getenv("STM_QA_CDP_PORT"); p != "" {
		return []string{"--remote-debugging-port=" + p}
	}
	return nil
}
