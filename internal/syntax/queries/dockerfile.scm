[
  "FROM" "AS" "RUN" "CMD" "LABEL" "EXPOSE" "ENV" "ADD" "COPY"
  "ENTRYPOINT" "VOLUME" "USER" "WORKDIR" "ARG" "ONBUILD"
  "STOPSIGNAL" "HEALTHCHECK" "SHELL" "MAINTAINER" "CROSS_BUILD"
  (heredoc_marker) (heredoc_end)
] @keyword

(comment) @comment

[
  (double_quoted_string)
  (single_quoted_string)
  (json_string)
  (heredoc_line)
] @string

(env_pair name: (_) @property)
(arg_instruction name: (_) @property)
(expansion (variable) @property)

(image_spec name: (image_name) @type)
(param) @attribute
(mount_param) @attribute
