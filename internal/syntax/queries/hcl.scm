; HCL / Terraform. The grammar ships no queries; this minimal set is
; hand-written against its src/node-types.json.
(comment) @comment
(numeric_lit) @number
(bool_lit) @boolean
(null_lit) @constant
(string_lit) @string
(quoted_template) @string
(heredoc_template) @string
(block (identifier) @type)
(attribute (identifier) @property)
(function_call (identifier) @function)
[
  "for"
  "in"
  "if"
  "else"
  "endfor"
  "endif"
] @keyword
