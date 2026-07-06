package tool

import "context"

type Weather struct{}

func (w Weather) Name() string {
	return "weather"
}

func (w Weather) Description() string {
	return "Get weather information for a given location."
}

func (w Weather) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "City name or location to check weather for",
			},
		},
		"required": []string{"location"},
	}
}

func (w Weather) Call(ctx context.Context, input string) (string, error) {
	return "天晴朗，27-35度，湿度50%", nil
}
