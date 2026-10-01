package gui

func (s *FyneScreen) cancelPlayTimer() {
	s.mu.Lock()
	cancel := s.cancelEnablePlay
	s.cancelEnablePlay = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
