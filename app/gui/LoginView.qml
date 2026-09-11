import QtQuick 2.9
import QtQuick.Controls 2.2
import QtQuick.Controls.Material 2.2
import QtQuick.Layouts 1.3

import ComputerManager 1.0
import GilCoordinator 1.0

Item {
    id: loginView
    objectName: qsTr("Welcome")
    focus: true

    Component.onCompleted: GilCoordinator.resumeSavedSession()

    Connections {
        target: GilCoordinator
        function onAssignedHost(address, port) {
            ComputerManager.addAssignedHost(address, port)
            stackView.replace("qrc:/gui/PcView.qml")
        }
    }

    Rectangle {
        anchors.fill: parent
        color: window.appBackground

        Rectangle {
            width: Math.min(parent.width * 0.42, 520)
            height: width
            radius: width / 2
            x: -width * 0.36
            y: -height * 0.48
            color: "#16c34ba9"
        }

        Rectangle {
            width: Math.min(parent.width * 0.32, 380)
            height: width
            radius: width / 2
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            anchors.rightMargin: -width * 0.42
            anchors.bottomMargin: -height * 0.56
            color: "#0fc34ba9"
        }
    }

    Rectangle {
        id: authCard
        anchors.centerIn: parent
        width: Math.min(parent.width - 48, 1040)
        height: Math.min(parent.height - 38, 460)
        radius: 28
        color: window.appSurface
        border.width: 1
        border.color: window.appBorder

        RowLayout {
            anchors.fill: parent
            anchors.margins: loginView.width >= 820 ? 42 : 30
            spacing: loginView.width >= 820 ? 42 : 0

            ColumnLayout {
                visible: loginView.width >= 820
                Layout.fillWidth: true
                Layout.fillHeight: true
                Layout.preferredWidth: 420
                spacing: 18

                RowLayout {
                    spacing: 14

                    Image {
                        Layout.preferredWidth: 64
                        Layout.preferredHeight: 64
                        source: "qrc:/res/gilstreaming-logo.png"
                        sourceSize.width: 128
                        sourceSize.height: 128
                        fillMode: Image.PreserveAspectFit
                    }

                    Label {
                        text: qsTr("GilStreaming")
                        color: window.brandAccentHover
                        font.pointSize: 21
                        font.bold: true
                    }
                }

                Item { Layout.preferredHeight: 4 }

                Label {
                    Layout.fillWidth: true
                    text: qsTr("Free PC.\nBro.")
                    color: window.appText
                    font.pointSize: 34
                    font.weight: Font.Medium
                    lineHeight: 0.95
                }

                Label {
                    Layout.fillWidth: true
                    Layout.maximumWidth: 390
                    text: qsTr("Sign into GilServers and enjoy a motherfucking free pc for gaming.")
                    color: window.appMutedText
                    font.pointSize: 13
                    lineHeight: 1.35
                    wrapMode: Text.Wrap
                }

                Item { Layout.fillHeight: true }

                Rectangle {
                    Layout.fillWidth: true
                    Layout.maximumWidth: 390
                    Layout.preferredHeight: 58
                    radius: 14
                    color: "#13c34ba9"
                    border.width: 1
                    border.color: "#45c34ba9"

                    RowLayout {
                        anchors.fill: parent
                        anchors.leftMargin: 16
                        anchors.rightMargin: 16
                        spacing: 12

                        Rectangle {
                            Layout.preferredWidth: 10
                            Layout.preferredHeight: 10
                            radius: 5
                            color: window.brandAccent
                        }

                        ColumnLayout {
                            Layout.fillWidth: true
                            spacing: 1

                            Label {
                                text: qsTr("GILSERVERS SECURED")
                                color: window.appMutedText
                                font.pixelSize: 10
                                font.bold: true
                                font.letterSpacing: 1.2
                            }

                            Label {
                                text: qsTr("One account across GilServices")
                                color: window.appText
                                font.pixelSize: 13
                                font.bold: true
                            }
                        }
                    }
                }
            }

            Rectangle {
                visible: loginView.width >= 820
                Layout.fillHeight: true
                Layout.preferredWidth: 1
                color: window.appBorder
            }

            ColumnLayout {
                Layout.fillWidth: true
                Layout.fillHeight: true
                Layout.preferredWidth: 440
                Layout.maximumWidth: 470
                Layout.alignment: Qt.AlignHCenter
                spacing: 16

                RowLayout {
                    visible: loginView.width < 820
                    Layout.alignment: Qt.AlignHCenter
                    spacing: 10

                    Image {
                        Layout.preferredWidth: 48
                        Layout.preferredHeight: 48
                        source: "qrc:/res/gilstreaming-logo.png"
                        sourceSize.width: 96
                        sourceSize.height: 96
                        fillMode: Image.PreserveAspectFit
                    }

                    Label {
                        text: qsTr("GilStreaming")
                        color: window.brandAccentHover
                        font.pointSize: 19
                        font.bold: true
                    }
                }

                Item { Layout.fillHeight: true }

                Label {
                    Layout.fillWidth: true
                    text: GilCoordinator.authenticated ? qsTr("Welcome back") : qsTr("Log in")
                    color: window.appText
                    font.pointSize: 25
                    font.bold: true
                    horizontalAlignment: Text.AlignLeft
                }

                Label {
                    Layout.fillWidth: true
                    text: GilCoordinator.authenticated
                          ? qsTr("We're finding the best available gaming VM for you.")
                          : qsTr("Continue with your GILid account.")
                    color: window.appMutedText
                    font.pointSize: 12
                    lineHeight: 1.35
                    wrapMode: Text.Wrap
                }

                Rectangle {
                    Layout.fillWidth: true
                    implicitHeight: statusLayout.implicitHeight + 28
                    radius: 14
                    color: window.appSurfaceRaised
                    border.width: 1
                    border.color: GilCoordinator.busy ? "#70c34ba9" : window.appBorder

                    RowLayout {
                        id: statusLayout
                        anchors.left: parent.left
                        anchors.right: parent.right
                        anchors.verticalCenter: parent.verticalCenter
                        anchors.leftMargin: 16
                        anchors.rightMargin: 16
                        spacing: 12

                        BusyIndicator {
                            Layout.preferredWidth: 28
                            Layout.preferredHeight: 28
                            visible: GilCoordinator.busy
                            running: visible
                        }

                        Rectangle {
                            visible: !GilCoordinator.busy
                            Layout.preferredWidth: 9
                            Layout.preferredHeight: 9
                            radius: 5
                            color: GilCoordinator.authenticated ? window.brandAccent : window.appMutedText
                        }

                        Label {
                            Layout.fillWidth: true
                            text: GilCoordinator.statusText
                            color: window.appMutedText
                            font.pointSize: 11
                            wrapMode: Text.Wrap
                        }
                    }
                }

                Button {
                    id: signInButton
                    Layout.fillWidth: true
                    Layout.preferredHeight: 52
                    text: qsTr("Continue with GILid")
                    visible: !GilCoordinator.authenticated
                    enabled: !GilCoordinator.busy
                    font.bold: true
                    onClicked: GilCoordinator.startLogin()

                    background: Rectangle {
                        radius: height / 2
                        color: !signInButton.enabled ? "#704b183f"
                               : signInButton.down ? window.brandAccentDark
                               : signInButton.hovered ? window.brandAccentHover
                               : window.brandAccent
                    }

                    contentItem: Label {
                        text: signInButton.text
                        color: "#fff8fd"
                        font: signInButton.font
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }

                Button {
                    id: retryButton
                    Layout.fillWidth: true
                    Layout.preferredHeight: 52
                    text: qsTr("Try another VM")
                    visible: GilCoordinator.authenticated && !GilCoordinator.busy
                    font.bold: true
                    onClicked: GilCoordinator.requestVm()

                    background: Rectangle {
                        radius: height / 2
                        color: retryButton.down ? window.brandAccentDark
                               : retryButton.hovered ? window.brandAccentHover
                               : window.brandAccent
                    }

                    contentItem: Label {
                        text: retryButton.text
                        color: "#fff8fd"
                        font: retryButton.font
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }

                Button {
                    id: developmentButton
                    Layout.fillWidth: true
                    Layout.preferredHeight: 44
                    text: qsTr("Skip login (development only)")
                    visible: GilCoordinator.developmentBuild
                    enabled: visible && !GilCoordinator.busy
                    flat: true
                    onClicked: GilCoordinator.skipLoginForDevelopment()

                    background: Rectangle {
                        radius: height / 2
                        color: developmentButton.hovered ? "#16ffffff" : "transparent"
                        border.width: 1
                        border.color: window.appBorder
                    }
                }

                Item { Layout.fillHeight: true }
            }
        }
    }
}
