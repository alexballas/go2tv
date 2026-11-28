package gui

// SetMediaType Method to implement the screen interface
func (p *FyneScreen) SetMediaType(mediaType string) {
	p.mu.Lock()
	p.castingMediaType = mediaType
	p.mu.Unlock()
}

func (p *FyneScreen) clearActiveDevice() {
	p.setActiveDevice(devType{})
}

// getScreenState returns the current screen state
func (p *FyneScreen) getScreenState() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.State
}

func (p *FyneScreen) nextChromecastActionID() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chromecastActionID++
	return p.chromecastActionID
}

func (p *FyneScreen) isChromecastActionCurrent(actionID uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.chromecastActionID == actionID
}
