;;; lip-mode.el --- Native editing support for LIP 0.6.4 -*- lexical-binding: t; -*-

;; Requires Emacs 27.1+; Flymake, JSON and imenu are built in.
;;; Commentary:
;; Add this directory to load-path and (require 'lip-mode).
;; M-x lip-check checks the saved file.  Flymake checks the current buffer,
;; including unsaved changes.  Completion is lexical, using generated spellings.
;;; Code:

(require 'cl-lib)
(require 'flymake)
(require 'json)
(require 'lip-keywords)

(defgroup lip nil "Edit LIP dependency programs." :group 'languages)
(defcustom lip-indent-offset 4
  "Number of spaces for each LIP indentation level."
  :type 'natnum :group 'lip)
(defcustom lipc-command "lipc"
  "LIP compiler executable name or full path; do not include shell arguments."
  :type 'string :group 'lip)
(defcustom lip-enable-flymake t
  "Enable background checking when the LIP compiler is available."
  :type 'boolean :group 'lip)

(defvar lip-mode-syntax-table
  (let ((table (make-syntax-table)))
    (modify-syntax-entry ?_ "w" table)
    (modify-syntax-entry ?# "<" table)
    (modify-syntax-entry ?\n ">" table)
    (modify-syntax-entry ?\" "\"" table)
    (modify-syntax-entry ?\\ "\\" table)
    ;; Integer division and logarithm are operators, never comments.
    (modify-syntax-entry ?/ "." table)
    (modify-syntax-entry ?* "." table)
    table))

(defconst lip--font-lock-keywords
  `((,(regexp-opt (cl-set-difference lip-language-keywords '("true" "false" "null") :test #'equal) 'symbols) . font-lock-keyword-face)
    (,(concat "\\_<" (regexp-opt lip-language-builtins t) "\\_>\\s-*(") 1 font-lock-builtin-face)
    (,(regexp-opt lip-language-types 'symbols) . font-lock-type-face)
    (,(regexp-opt '("true" "false" "null") 'symbols) . font-lock-constant-face)
    ("\\_<\\(?:flow\\|fn\\)\\s-+\\([[:alpha:]_][[:alnum:]_]*\\)\\s-*(" 1 font-lock-function-name-face)
    ("\\_<[0-9]+\\(?:\\.[0-9]*\\)?\\(?:[eE][+-]?[0-9]+\\)?" . font-lock-constant-face)
    (,(regexp-opt lip-language-operators) . font-lock-builtin-face)))

(defvar lip-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map (kbd "C-c C-c") #'lip-check)
    map))

(defun lip--previous-arrow-indent ()
  "Return continuation indentation after a preceding branch arrow, or nil."
  (save-excursion
    (let (found)
      (while (and (not found) (= (forward-line -1) 0))
        (let* ((end (line-end-position))
               (state (syntax-ppss end)))
          (when (nth 4 state) (setq end (nth 8 state)))
          (let ((code (buffer-substring-no-properties (line-beginning-position) end)))
            (when (string-match-p "\\S-" code)
              (setq found t)
              (when (and (string-match "=>[ \t]*$" code)
                         (not (nth 3 (syntax-ppss (+ (line-beginning-position) (match-beginning 0))))))
                (setq found (+ (current-indentation) lip-indent-offset)))))))
      (and (numberp found) found))))

(defun lip-indent-line ()
  "Indent using nested delimiters, ignoring comments and strings."
  (interactive)
  (let* ((offset (- (point) (line-beginning-position)))
         (state (syntax-ppss (line-beginning-position)))
         (opener (nth 1 state))
         (closing (save-excursion (back-to-indentation) (looking-at-p "[]})]")))
         (target
          (if (nth 3 state) (current-indentation)
            (or (and (not closing) (lip--previous-arrow-indent))
                (if opener
                    (+ (save-excursion (goto-char opener) (current-indentation))
                       (if closing 0 lip-indent-offset))
                  0)))))
    (indent-line-to target)
    (when (> offset (current-indentation))
      (move-to-column offset))))

(defun lip-completion-at-point ()
  "Complete current words and qualified builtin operation names."
  (let ((end (point))
        (start (save-excursion (skip-chars-backward "[:alnum:]_.") (point))))
    (unless (nth 8 (syntax-ppss))
      (let ((words (append lip-language-keywords lip-language-types lip-language-builtins)))
        (save-excursion
          (goto-char (point-min))
          (while (re-search-forward "\\_<[[:alpha:]_][[:alnum:]_]*\\_>" nil t)
            (unless (save-excursion (save-match-data (nth 8 (syntax-ppss (match-beginning 0)))))
              (push (match-string-no-properties 0) words))))
        (list start end (delete-dups words) :exclusive 'no)))))

(defun lip-check ()
  "Save and check this file, displaying navigable compiler diagnostics."
  (interactive)
  (unless buffer-file-name (user-error "Save this buffer to a .lip file first"))
  (unless (executable-find lipc-command) (user-error "Compiler unavailable: %s" lipc-command))
  (save-buffer)
  (flymake-mode 1)
  (flymake-start nil t)
  (if (fboundp 'flymake-show-buffer-diagnostics)
      (flymake-show-buffer-diagnostics)
    ;; Compatibility with older Flymake versions; newer versions rename this API.
    (with-no-warnings (flymake-show-diagnostics-buffer))))

(defvar-local lip--check-process nil)

(defun lip--cancel-check ()
  "Cancel any check owned by this buffer."
  (let ((process lip--check-process))
    (setq lip--check-process nil)
    (when (and (processp process) (process-live-p process))
      (delete-process process))))

(defun lip--diagnostic-region (line column)
  "Convert the compiler's one-based Unicode LINE and COLUMN to buffer positions."
  (save-restriction
    (widen)
    (save-excursion
      (goto-char (point-min))
      (forward-line (1- (max 1 (or line 1))))
      (forward-char (min (1- (max 1 (or column 1))) (- (line-end-position) (point))))
      (cons (point) (min (point-max) (1+ (point)))))))

(defun lip--clean-check (output temporary)
  "Remove an asynchronous check's OUTPUT buffer and TEMPORARY file."
  (when (buffer-live-p output) (kill-buffer output))
  (when (and temporary (file-exists-p temporary)) (delete-file temporary)))

(defun lip--finish-check (process source-buffer source-tick output temporary report-fn)
  "Report a completed PROCESS only if SOURCE-BUFFER still matches SOURCE-TICK."
  (when (memq (process-status process) '(exit signal))
    (unwind-protect
        (when (buffer-live-p source-buffer)
          (with-current-buffer source-buffer
            (when (eq process lip--check-process)
              (setq lip--check-process nil)
              (when (= source-tick (buffer-chars-modified-tick))
                (condition-case parse-error
                    (let* ((report (with-current-buffer output
                                     (goto-char (point-min))
                                     (json-parse-buffer :object-type 'alist :array-type 'list)))
                           (diagnostics (alist-get 'diagnostics report))
                           (items nil))
                      (unless (equal (alist-get 'schema report) "lip.diagnostics.v1")
                        (error "Unexpected diagnostic schema"))
                      (when (and (/= (process-exit-status process) 0) (null diagnostics))
                        (error "Compiler failed without diagnostics"))
                      (dolist (item diagnostics)
                        (let ((region (lip--diagnostic-region (alist-get 'line item) (alist-get 'column item))))
                          (push (flymake-make-diagnostic
                                 source-buffer (car region) (cdr region) :error
                                 (concat (alist-get 'code item) ": " (alist-get 'message item)
                                         (when (alist-get 'hints item)
                                           (concat "\n" (mapconcat #'identity (alist-get 'hints item) "\n")))))
                                items)))
                      (funcall report-fn (nreverse items)))
                  (error (funcall report-fn :panic :explanation (error-message-string parse-error))))))))
      (lip--clean-check output temporary))))

(defun lip-flymake (report-fn &rest _args)
  "Check the current buffer asynchronously and report through REPORT-FN."
  (lip--cancel-check)
  (if (not (executable-find lipc-command))
      (funcall report-fn nil)
    (let ((source-buffer (current-buffer))
          (source-tick (buffer-chars-modified-tick))
          temporary output)
      (condition-case err
          (progn
            (setq temporary (make-temp-file "lip-flymake-" nil ".lip")
                  output (generate-new-buffer " *lip-flymake*"))
            (let ((coding-system-for-write 'utf-8-unix))
              (save-restriction (widen) (write-region (point-min) (point-max) temporary nil 'silent)))
            (setq lip--check-process
                  (make-process
                   :name "lip-flymake" :buffer output :connection-type 'pipe :noquery t :coding 'utf-8-unix
                   :command (list lipc-command "check" "--json" temporary)
                   :sentinel (lambda (process _event)
                               (lip--finish-check process source-buffer source-tick output temporary report-fn)))))
        (error
         (lip--clean-check output temporary)
         (funcall report-fn :panic :explanation (error-message-string err)))))))

;;;###autoload
(define-derived-mode lip-mode prog-mode "LIP"
  "Edit LIP programs, with completion and optional compiler-backed diagnostics."
  :syntax-table lip-mode-syntax-table
  (setq-local font-lock-defaults '(lip--font-lock-keywords))
  (setq-local indent-line-function #'lip-indent-line)
  (setq-local indent-tabs-mode nil)
  (setq-local comment-start "# ")
  (setq-local comment-end "")
  (setq-local comment-start-skip "#+[ \t]*")
  (setq-local imenu-generic-expression
              '(("Flows" "^[ \t]*flow[ \t]+\\([[:alpha:]_][[:alnum:]_]*\\)" 1)
                ("Functions" "^[ \t]*fn[ \t]+\\([[:alpha:]_][[:alnum:]_]*\\)" 1)))
  (add-hook 'completion-at-point-functions #'lip-completion-at-point nil t)
  (add-hook 'flymake-diagnostic-functions #'lip-flymake nil t)
  (add-hook 'kill-buffer-hook #'lip--cancel-check nil t)
  (add-hook 'change-major-mode-hook #'lip--cancel-check nil t)
  (when (and lip-enable-flymake (executable-find lipc-command)) (flymake-mode 1)))

;;;###autoload
(add-to-list 'auto-mode-alist '("\\.lip\\'" . lip-mode))
(provide 'lip-mode)
;;; lip-mode.el ends here
