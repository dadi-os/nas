import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Item {
    id: root
    signal saved(string message)

    property string status: ""

    function load() {
        status = ""
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Failed to load dwar settings (" + xhr.status + ")"
                return
            }
            try {
                apply(JSON.parse(xhr.responseText))
            } catch (e) {
                status = "Bad settings payload"
            }
        }
        xhr.open("GET", "http://127.0.0.1:8092/modules/dwar/settings")
        xhr.send()
    }

    function apply(data) {
        const env = data.env || {}
        anthropic.text = env.ANTHROPIC_API_KEY || ""
        gemini.text = env.GEMINI_API_KEY || ""
        openai.text = env.OPENAI_API_KEY || ""
        deepgram.text = env.DEEPGRAM_API_KEY || ""
        const c = data.config || {}
        const r = (c.chat && c.chat.reasoning) || {}
        reasonProvider.text = r.provider || ""
        reasonModel.text = r.model || ""
        reasonTokens.text = r.max_tokens !== undefined ? String(r.max_tokens) : ""
        reasonThink.text = r.thinking_budget !== undefined ? String(r.thinking_budget) : ""
        const conv = (c.chat && c.chat.conversation) || {}
        convProvider.text = conv.provider || ""
        convModel.text = conv.model || ""
        convTokens.text = conv.max_tokens !== undefined ? String(conv.max_tokens) : ""
        const em = c.embed || {}
        embedProvider.text = em.provider || ""
        embedModel.text = em.model || ""
        embedDim.text = em.dimensions !== undefined ? String(em.dimensions) : ""
        embedBatch.text = em.max_batch_size !== undefined ? String(em.max_batch_size) : ""
        embedLen.text = em.max_text_length !== undefined ? String(em.max_text_length) : ""
        const desc = (c.image && c.image.describe) || {}
        descProvider.text = desc.provider || ""
        descModel.text = desc.model || ""
        descTokens.text = desc.max_tokens !== undefined ? String(desc.max_tokens) : ""
        descBytes.text = desc.max_bytes !== undefined ? String(desc.max_bytes) : ""
        descPrompt.text = desc.max_prompt_length !== undefined ? String(desc.max_prompt_length) : ""
        descTypes.text = (desc.allowed_media_types || []).join(", ")
        const create = (c.image && c.image.create) || {}
        createProvider.text = create.provider || ""
        createModel.text = create.model || ""
        createPrompt.text = create.max_prompt_length !== undefined ? String(create.max_prompt_length) : ""
        const sp = (c.speech && c.speech.transcribe) || {}
        speechProvider.text = sp.provider || ""
        speechModel.text = sp.model || ""
        speechLang.text = sp.language || ""
        speechBytes.text = sp.max_bytes !== undefined ? String(sp.max_bytes) : ""
        speechTypes.text = (sp.allowed_media_types || []).join(", ")
        const retry = c.retry || {}
        retryAttempts.text = retry.attempts !== undefined ? String(retry.attempts) : ""
        retryBackoff.text = (retry.backoff_seconds || []).join(", ")
        retryTimeout.text = retry.timeout_seconds !== undefined ? String(retry.timeout_seconds) : ""
    }

    function csv(text) {
        return text.split(",").map(function (s) { return s.trim() }).filter(function (s) { return s !== "" })
    }

    function nums(text) {
        return csv(text).map(function (s) { return Number(s) }).filter(function (n) { return !isNaN(n) })
    }

    function payload() {
        return {
            env: {
                ANTHROPIC_API_KEY: anthropic.text,
                GEMINI_API_KEY: gemini.text,
                OPENAI_API_KEY: openai.text,
                DEEPGRAM_API_KEY: deepgram.text
            },
            config: {
                chat: {
                    reasoning: {
                        provider: reasonProvider.text,
                        model: reasonModel.text,
                        max_tokens: Number(reasonTokens.text),
                        thinking_budget: Number(reasonThink.text)
                    },
                    conversation: {
                        provider: convProvider.text,
                        model: convModel.text,
                        max_tokens: Number(convTokens.text)
                    }
                },
                embed: {
                    provider: embedProvider.text,
                    model: embedModel.text,
                    dimensions: Number(embedDim.text),
                    max_batch_size: Number(embedBatch.text),
                    max_text_length: Number(embedLen.text)
                },
                image: {
                    describe: {
                        provider: descProvider.text,
                        model: descModel.text,
                        max_tokens: Number(descTokens.text),
                        max_bytes: Number(descBytes.text),
                        max_prompt_length: Number(descPrompt.text),
                        allowed_media_types: csv(descTypes.text)
                    },
                    create: {
                        provider: createProvider.text,
                        model: createModel.text,
                        max_prompt_length: Number(createPrompt.text)
                    }
                },
                speech: {
                    transcribe: {
                        provider: speechProvider.text,
                        model: speechModel.text,
                        language: speechLang.text,
                        max_bytes: Number(speechBytes.text),
                        allowed_media_types: csv(speechTypes.text)
                    }
                },
                retry: {
                    attempts: Number(retryAttempts.text),
                    backoff_seconds: nums(retryBackoff.text),
                    timeout_seconds: Number(retryTimeout.text)
                }
            }
        }
    }

    function save() {
        status = "Saving…"
        const xhr = new XMLHttpRequest()
        xhr.onreadystatechange = function () {
            if (xhr.readyState !== XMLHttpRequest.DONE)
                return
            if (xhr.status !== 200) {
                status = "Save failed (" + xhr.status + ")"
                return
            }
            status = ""
            root.saved("dwar saved · restarted")
        }
        xhr.open("PUT", "http://127.0.0.1:8092/modules/dwar/settings")
        xhr.setRequestHeader("Content-Type", "application/json")
        xhr.send(JSON.stringify(payload()))
    }

    Component.onCompleted: load()
    onVisibleChanged: if (visible) load()

    Flickable {
        anchors.fill: parent
        contentWidth: width
        contentHeight: col.height
        clip: true
        boundsBehavior: Flickable.StopAtBounds

        ColumnLayout {
            id: col
            width: parent.width
            spacing: 14

            Text {
                text: "Dwar"
                color: "#2c302a"
                font.pixelSize: 20
                font.weight: Font.DemiBold
            }
            Text {
                text: "Provider keys and model routing. Saving restarts dwar."
                color: "#6e7568"
                font.pixelSize: 13
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            Text { text: "KEYS"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 4 }
            FormRow { id: anthropic; label: "Anthropic"; secret: true }
            FormRow { id: gemini; label: "Gemini"; secret: true }
            FormRow { id: openai; label: "OpenAI"; secret: true }
            FormRow { id: deepgram; label: "Deepgram"; secret: true }

            Text { text: "REASONING"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 8 }
            FormRow { id: reasonProvider; label: "Provider" }
            FormRow { id: reasonModel; label: "Model" }
            FormRow { id: reasonTokens; label: "Max tokens" }
            FormRow { id: reasonThink; label: "Thinking budget" }

            Text { text: "CONVERSATION"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 8 }
            FormRow { id: convProvider; label: "Provider" }
            FormRow { id: convModel; label: "Model" }
            FormRow { id: convTokens; label: "Max tokens" }

            Text { text: "EMBED"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 8 }
            FormRow { id: embedProvider; label: "Provider" }
            FormRow { id: embedModel; label: "Model" }
            FormRow { id: embedDim; label: "Dimensions" }
            FormRow { id: embedBatch; label: "Max batch size" }
            FormRow { id: embedLen; label: "Max text length" }

            Text { text: "IMAGE DESCRIBE"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 8 }
            FormRow { id: descProvider; label: "Provider" }
            FormRow { id: descModel; label: "Model" }
            FormRow { id: descTokens; label: "Max tokens" }
            FormRow { id: descBytes; label: "Max bytes" }
            FormRow { id: descPrompt; label: "Max prompt length" }
            FormRow { id: descTypes; label: "Allowed media types"; hint: "Comma-separated MIME types" }

            Text { text: "IMAGE CREATE"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 8 }
            FormRow { id: createProvider; label: "Provider" }
            FormRow { id: createModel; label: "Model" }
            FormRow { id: createPrompt; label: "Max prompt length" }

            Text { text: "SPEECH"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 8 }
            FormRow { id: speechProvider; label: "Provider" }
            FormRow { id: speechModel; label: "Model" }
            FormRow { id: speechLang; label: "Language" }
            FormRow { id: speechBytes; label: "Max bytes" }
            FormRow { id: speechTypes; label: "Allowed media types"; hint: "Comma-separated MIME types" }

            Text { text: "RETRY"; color: "#5c6b52"; font.pixelSize: 10; font.letterSpacing: 2; Layout.topMargin: 8 }
            FormRow { id: retryAttempts; label: "Attempts" }
            FormRow { id: retryBackoff; label: "Backoff seconds"; hint: "Comma-separated" }
            FormRow { id: retryTimeout; label: "Timeout seconds" }

            Button {
                Layout.preferredHeight: 40
                Layout.preferredWidth: 160
                Layout.topMargin: 8
                onClicked: root.save()
                background: Rectangle {
                    radius: 9
                    color: parent.down ? "#5c6b52" : "#8fa382"
                }
                contentItem: Text {
                    text: "Save"
                    color: "#fafaf7"
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                    font.pixelSize: 13
                }
            }

            Text {
                text: status
                color: "#b56b5c"
                font.pixelSize: 12
                visible: status !== ""
                wrapMode: Text.WordWrap
                Layout.fillWidth: true
            }

            Item { Layout.preferredHeight: 8 }
        }
    }
}
