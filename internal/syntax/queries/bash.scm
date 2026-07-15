; Commands

(command_name (word) @function)

(function_definition
  name: (word) @function)

; Variables

(variable_name) @property
(special_variable_name) @property
(file_descriptor) @number

; Keywords

[
  "if"
  "then"
  "else"
  "elif"
  "fi"
  "case"
  "esac"
  "for"
  "select"
  "while"
  "until"
  "do"
  "done"
  "in"
  "function"
  "declare"
  "typeset"
  "export"
  "readonly"
  "local"
  "unset"
  "unsetenv"
] @keyword

; Literals

[
  (string)
  (raw_string)
  (ansi_c_string)
  (heredoc_body)
  (heredoc_start)
] @string

(number) @number

(comment) @comment

(test_operator) @operator
