import QtQuick 2.9

Rectangle {
    id: background
    color: window.appBackground
    clip: true

    Rectangle {
        id: glowTop
        width: Math.max(background.width * 0.62, 560)
        height: width
        radius: width / 2
        x: -width * 0.34
        y: -height * 0.52
        color: "#18c34ba9"

        SequentialAnimation on x {
            running: background.visible && window.visible
            loops: Animation.Infinite
            NumberAnimation { to: -glowTop.width * 0.18; duration: 9000; easing.type: Easing.InOutSine }
            NumberAnimation { to: -glowTop.width * 0.34; duration: 9000; easing.type: Easing.InOutSine }
        }
        SequentialAnimation on y {
            running: background.visible && window.visible
            loops: Animation.Infinite
            NumberAnimation { to: -glowTop.height * 0.38; duration: 12000; easing.type: Easing.InOutSine }
            NumberAnimation { to: -glowTop.height * 0.52; duration: 12000; easing.type: Easing.InOutSine }
        }
    }

    Rectangle {
        id: glowBottom
        width: Math.max(background.width * 0.48, 430)
        height: width
        radius: width / 2
        x: background.width - width * 0.58
        y: background.height - height * 0.42
        color: "#106f3b78"

        SequentialAnimation on x {
            running: background.visible && window.visible
            loops: Animation.Infinite
            NumberAnimation { to: background.width - glowBottom.width * 0.76; duration: 11000; easing.type: Easing.InOutSine }
            NumberAnimation { to: background.width - glowBottom.width * 0.58; duration: 11000; easing.type: Easing.InOutSine }
        }
        SequentialAnimation on y {
            running: background.visible && window.visible
            loops: Animation.Infinite
            NumberAnimation { to: background.height - glowBottom.height * 0.62; duration: 14000; easing.type: Easing.InOutSine }
            NumberAnimation { to: background.height - glowBottom.height * 0.42; duration: 14000; easing.type: Easing.InOutSine }
        }
    }

    Rectangle {
        id: accentGlow
        width: Math.max(background.width * 0.3, 300)
        height: width
        radius: width / 2
        x: background.width * 0.5 - width / 2
        y: background.height * 0.26 - height / 2
        color: "#087d456f"

        SequentialAnimation on opacity {
            running: background.visible && window.visible
            loops: Animation.Infinite
            NumberAnimation { to: 0.35; duration: 6000; easing.type: Easing.InOutSine }
            NumberAnimation { to: 1.0; duration: 6000; easing.type: Easing.InOutSine }
        }
        SequentialAnimation on scale {
            running: background.visible && window.visible
            loops: Animation.Infinite
            NumberAnimation { to: 1.16; duration: 8000; easing.type: Easing.InOutSine }
            NumberAnimation { to: 1.0; duration: 8000; easing.type: Easing.InOutSine }
        }
    }

    Rectangle {
        anchors.fill: parent
        color: "#26000000"
    }
}
