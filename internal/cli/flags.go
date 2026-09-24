// Flag parsing with the API 2 invocation rules: long command options plus the
// conventional -h and -j aliases, `--` ends option parsing, unknown flags
// and duplicate scalar flags are errors.
package cli

import (
	"errors"
	"strconv"

	"github.com/toolsupply/ticket/internal/contract"
)

var errParserMetadata = errors.New("parser metadata")

type flagKind int

const (
	kindString flagKind = iota
	kindInt
	kindBool
	kindRepeat
	kindTail
)

type flagDef struct {
	kind flagKind
	set  func(value string) error
	tail func(values []string) error
	used *bool
}

type parser struct {
	defs         map[string]*flagDef
	aliases      map[string]string
	positionals  []string
	help         *bool
	helpSeen     *bool
	metadataOnly bool
}

// flag registers one command-specific option.
func (p *parser) flag(name string, kind flagKind, set func(value string) error, repeatable bool) {
	if p.defs == nil {
		p.defs = map[string]*flagDef{}
	}
	p.defs[name] = &flagDef{kind: kind, set: set, used: new(bool)}
}

// alias registers a short spelling for an existing long option.
func (p *parser) alias(short, name string) {
	if p.aliases == nil {
		p.aliases = map[string]string{}
	}
	p.aliases[short] = name
}

func (p *parser) str(name string, dst *string) {
	p.flag(name, kindString, func(v string) error { *dst = v; return nil }, false)
}

func (p *parser) intValue(name string, dst *int, has *bool) {
	p.flag(name, kindInt, func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return contract.NewError(contract.ErrInvalidArgument, "Flag --"+name+" expects an integer.", nil)
		}
		*dst = n
		if has != nil {
			*has = true
		}
		return nil
	}, false)
}

func (p *parser) boolValue(name string, dst *bool) {
	p.flag(name, kindBool, func(v string) error {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return contract.NewError(contract.ErrInvalidArgument,
				"Flag --"+name+" is a boolean flag.", nil)
		}
		*dst = b
		return nil
	}, false)
}

func (p *parser) repeat(name string, dst *[]string) {
	p.flag(name, kindRepeat, func(v string) error { *dst = append(*dst, v); return nil }, true)
}

// tail registers an option whose presence makes every remaining argv token
// belong to the option value. It is used by parser-aware multi-token options
// such as -q/--query; unlike a normal string option it never consumes only
// the next token and never resumes option parsing.
func (p *parser) tail(name string, dst *[]string, presence ...*bool) {
	if p.defs == nil {
		p.defs = map[string]*flagDef{}
	}
	p.defs[name] = &flagDef{
		kind: kindTail,
		tail: func(values []string) error {
			if len(presence) > 0 && presence[0] != nil {
				*presence[0] = true
			}
			*dst = append(*dst, values...)
			return nil
		},
		used: new(bool),
	}
}

func (p *parser) parse(args []string) error {
	if p.metadataOnly {
		return errParserMetadata
	}
	i := 0
	restArePositionals := false
	for i < len(args) {
		arg := args[i]
		if !restArePositionals && arg == "--" {
			restArePositionals = true
			i++
			continue
		}
		if !restArePositionals && arg == "-h" {
			if *p.helpSeen {
				return duplicateFlag("help")
			}
			*p.helpSeen = true
			*p.help = true
			i++
			continue
		}
		if !restArePositionals && arg == "-j" {
			def, ok := p.defs["json"]
			if !ok {
				return contract.NewError(contract.ErrInvalidArgument, "Unknown flag -j.", map[string]any{"flag": "-j"})
			}
			if *def.used {
				return duplicateFlag("json")
			}
			*def.used = true
			if err := def.set("true"); err != nil {
				return err
			}
			i++
			continue
		}
		if !restArePositionals && len(arg) == 2 && arg[0] == '-' {
			name, ok := p.aliases[arg[1:]]
			if !ok {
				return contract.NewError(contract.ErrInvalidArgument,
					"Unknown flag "+arg+".", map[string]any{"flag": arg})
			}
			def := p.defs[name]
			i++
			if def.kind == kindTail {
				if *def.used {
					return duplicateFlag(name)
				}
				*def.used = true
				if err := def.tail(args[i:]); err != nil {
					return err
				}
				return nil
			}
			if def.kind == kindBool {
				if *def.used {
					return duplicateFlag(name)
				}
				*def.used = true
				if err := def.set("true"); err != nil {
					return err
				}
				continue
			}
			if i >= len(args) {
				return contract.NewError(contract.ErrInvalidArgument,
					"Flag --"+name+" requires a value.", map[string]any{"flag": arg})
			}
			if def.kind != kindRepeat && *def.used {
				return duplicateFlag(name)
			}
			*def.used = true
			if err := def.set(args[i]); err != nil {
				return err
			}
			i++
			continue
		}
		if !restArePositionals && len(arg) > 2 && arg[:2] == "--" {
			name := arg[2:]
			value := ""
			hasValue := false
			if eq := indexByte(name, '='); eq >= 0 {
				value = name[eq+1:]
				name = name[:eq]
				hasValue = true
			}
			if name == "help" {
				if *p.helpSeen {
					return duplicateFlag("help")
				}
				*p.helpSeen = true
				*p.help = true
				i++
				continue
			}
			def, ok := p.defs[name]
			if !ok {
				return contract.NewError(contract.ErrInvalidArgument,
					"Unknown flag --"+name+".", map[string]any{"flag": "--" + name})
			}
			if def.kind == kindTail {
				if hasValue {
					return contract.NewError(contract.ErrInvalidArgument,
						"Flag --"+name+" does not accept an inline value.", map[string]any{"flag": "--" + name})
				}
				if *def.used {
					return duplicateFlag(name)
				}
				*def.used = true
				if err := def.tail(args[i+1:]); err != nil {
					return err
				}
				return nil
			}
			if def.kind == kindBool {
				if hasValue {
					v, err := strconv.ParseBool(value)
					if err != nil {
						return contract.NewError(contract.ErrInvalidArgument,
							"Flag --"+name+" is a boolean flag.", nil)
					}
					value = strconv.FormatBool(v)
				} else {
					value = "true"
				}
			} else if !hasValue {
				i++
				if i >= len(args) {
					return contract.NewError(contract.ErrInvalidArgument,
						"Flag --"+name+" requires a value.", map[string]any{"flag": "--" + name})
				}
				value = args[i]
			}
			if def.kind != kindRepeat && *def.used {
				return duplicateFlag(name)
			}
			*def.used = true
			if err := def.set(value); err != nil {
				return err
			}
			i++
			continue
		}
		p.positionals = append(p.positionals, arg)
		i++
	}
	return nil
}

func (p *parser) requireNoPositionals(what string) error {
	if len(p.positionals) > 0 {
		return contract.NewError(contract.ErrInvalidArgument,
			"Command "+what+" accepts no arguments.", nil)
	}
	return nil
}

func duplicateFlag(name string) *contract.Error {
	return contract.NewError(contract.ErrInvalidArgument,
		"Duplicate flag --"+name+".", map[string]any{"flag": "--" + name})
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
