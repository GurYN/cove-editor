; Selectors

(tag_name) @tag
(class_name) @property
(id_name) @property
(attribute_name) @attribute
(pseudo_class_selector (class_name) @keyword)
(pseudo_element_selector (tag_name) @keyword)
(namespace_name) @property

; Declarations

(property_name) @property
(feature_name) @property
(function_name) @function

; At-rules

(at_keyword) @keyword
"@media" @keyword
"@import" @keyword
"@charset" @keyword
"@namespace" @keyword
"@supports" @keyword
"@keyframes" @keyword
(to) @keyword
(from) @keyword
(important) @keyword

; Values

(string_value) @string
(integer_value) @number
(float_value) @number
(unit) @type
(color_value) @constant
(plain_value) @constant

(comment) @comment
