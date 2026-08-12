//go:build !linux

package browser

func readRendererProcessSamples(int) ([]rendererProcessSample, error) {
	return nil, nil
}
