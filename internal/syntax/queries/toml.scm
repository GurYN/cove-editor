(bare_key) @property
(quoted_key) @property

(table (bare_key) @type)
(table (dotted_key (bare_key) @type))
(table_array_element (bare_key) @type)
(table_array_element (dotted_key (bare_key) @type))

(string) @string
(escape_sequence) @escape
(integer) @number
(float) @number
(boolean) @constant.builtin
(offset_date_time) @constant
(local_date_time) @constant
(local_date) @constant
(local_time) @constant

(comment) @comment
