; Types

(type_identifier) @type
(predefined_type) @type.builtin

((identifier) @type
 (#match? @type "^[A-Z]"))

(type_arguments
  "<" @punctuation.bracket
  ">" @punctuation.bracket)

; Variables

(required_parameter (identifier) @variable.parameter)
(optional_parameter (identifier) @variable.parameter)

; Functions

(function_declaration
  name: (identifier) @function)

(method_definition
  name: (property_identifier) @function.method)

(call_expression
  function: (identifier) @function)

(call_expression
  function: (member_expression
    property: (property_identifier) @function.method))

; Properties

(property_identifier) @property

; Literals

[
  (string)
  (template_string)
  (regex)
] @string

(escape_sequence) @escape

(number) @number

[
  (true)
  (false)
  (null)
  (undefined)
] @constant.builtin

(comment) @comment

; Keywords

[ "async"
  "await"
  "break"
  "case"
  "catch"
  "class"
  "const"
  "continue"
  "default"
  "delete"
  "do"
  "else"
  "extends"
  "finally"
  "for"
  "function"
  "if"
  "import"
  "in"
  "instanceof"
  "let"
  "new"
  "of"
  "return"
  "static"
  "switch"
  "throw"
  "try"
  "typeof"
  "var"
  "void"
  "while"
  "yield"
] @keyword

[ "abstract"
  "declare"
  "enum"
  "export"
  "implements"
  "interface"
  "keyof"
  "namespace"
  "private"
  "protected"
  "public"
  "type"
  "readonly"
  "override"
  "satisfies"
] @keyword
