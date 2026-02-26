package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"soasurs.dev/soasurs/adk/pkg/tool"
)

func ShellTool() tool.Tool {
	return tool.NewTool(
		"shell",
		"Execute shell commands (use with caution)",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Command timeout in seconds",
				},
				"shell": map[string]any{
					"type":        "string",
					"description": "Shell to use (default: system default)",
				},
			},
			"required": []string{"command"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			command, ok := args["command"].(string)
			if !ok {
				return nil, fmt.Errorf("command is required and must be a string")
			}

			timeout := 30 * time.Second
			if t, ok := args["timeout"].(int); ok {
				timeout = time.Duration(t) * time.Second
			}

			shell := ""
			if s, ok := args["shell"].(string); ok {
				shell = s
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			var cmd *exec.Cmd
			if shell != "" {
				cmd = exec.CommandContext(ctx, shell, "-c", command)
			} else {
				switch runtime.GOOS {
				case "windows":
					cmd = exec.CommandContext(ctx, "cmd", "/C", command)
				default:
					cmd = exec.CommandContext(ctx, "sh", "-c", command)
				}
			}

			output, err := cmd.CombinedOutput()
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return nil, fmt.Errorf("command timed out after %v", timeout)
				}
				return map[string]any{
					"output": string(output),
					"error":  err.Error(),
					"code":   cmd.ProcessState.ExitCode(),
				}, nil
			}

			return map[string]any{
				"output": string(output),
				"code":   0,
			}, nil
		},
	)
}

func EchoTool() tool.Tool {
	return tool.NewTool(
		"echo",
		"Echo back the input message (useful for testing)",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to echo back",
				},
			},
			"required": []string{"message"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			message, ok := args["message"].(string)
			if !ok {
				return nil, fmt.Errorf("message is required and must be a string")
			}
			return map[string]any{
				"echo": message,
				"time": time.Now().Format(time.RFC3339),
			}, nil
		},
	)
}

func CalculatorTool() tool.Tool {
	return tool.NewTool(
		"calculator",
		"Perform basic arithmetic calculations",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "Mathematical expression to evaluate (e.g., '2 + 2 * 3')",
				},
			},
			"required": []string{"expression"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			expression, ok := args["expression"].(string)
			if !ok {
				return nil, fmt.Errorf("expression is required and must be a string")
			}

			expression = strings.ReplaceAll(expression, " ", "")

			result, err := evaluateExpression(expression)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"expression": expression,
				"result":     result,
			}, nil
		},
	)
}

func evaluateExpression(expr string) (float64, error) {
	if len(expr) == 0 {
		return 0, fmt.Errorf("empty expression")
	}

	numbers := make([]float64, 0)
	operators := make([]rune, 0)
	currentNum := ""

	for _, ch := range expr {
		if ch >= '0' && ch <= '9' || ch == '.' {
			currentNum += string(ch)
		} else if ch == '+' || ch == '-' || ch == '*' || ch == '/' {
			if currentNum != "" {
				num, err := parseFloat(currentNum)
				if err != nil {
					return 0, err
				}
				numbers = append(numbers, num)
				currentNum = ""
			}
			operators = append(operators, ch)
		} else {
			return 0, fmt.Errorf("invalid character: %c", ch)
		}
	}

	if currentNum != "" {
		num, err := parseFloat(currentNum)
		if err != nil {
			return 0, err
		}
		numbers = append(numbers, num)
	}

	if len(numbers) != len(operators)+1 {
		return 0, fmt.Errorf("invalid expression format")
	}

	result := numbers[0]
	for i, op := range operators {
		switch op {
		case '+':
			result += numbers[i+1]
		case '-':
			result -= numbers[i+1]
		case '*':
			result *= numbers[i+1]
		case '/':
			if numbers[i+1] == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			result /= numbers[i+1]
		}
	}

	return result, nil
}

func parseFloat(s string) (float64, error) {
	var result float64
	var decimal float64
	var decimalPlace float64 = 1
	var isDecimal bool

	for _, ch := range s {
		if ch == '.' {
			isDecimal = true
			continue
		}
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid number: %s", s)
		}
		digit := float64(ch - '0')
		if isDecimal {
			decimalPlace *= 10
			decimal += digit / decimalPlace
		} else {
			result = result*10 + digit
		}
	}

	return result + decimal, nil
}
