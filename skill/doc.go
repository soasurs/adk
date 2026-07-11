// Package skill loads, validates, and exposes Agent Skills.
//
// It implements the SKILL.md format and progressive-disclosure helpers without
// coupling skills to a particular agent implementation. Applications choose
// how to inject a catalog instruction and which skill tools to register.
package skill
