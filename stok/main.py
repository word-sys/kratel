#word-sys

import sys
from PyQt5 import QtWidgets
from PyQt5.QtWidgets import *
from Urun_Ekle import *
from subprocess import call

uygulama = QApplication(sys.argv)
pencere = QMainWindow()
ui = Ui_MainWindow()
ui.setupUi(pencere)
pencere.show()
import sqlite3

baglanti = sqlite3.connect("urunler.db")
islem = baglanti.cursor()
baglanti.commit()

table = islem.execute("create table if not exists urun (urunKodu int, urunAdi text, dolapNo int, stokMiktari int, urunAciklaması text, odaNo int, kat int)")
baglanti.commit()


def kayit_ekle():
    UrunKodu = int(ui.lneurunKodu.text())
    UrunAdi = ui.lneurunAdi.text()
    DolapNo = int(ui.lnedolapNo.text())
    StokMiktari = int(ui.lnestokMiktari.text())
    UrunAciklama = ui.lneurunAciklama.text()
    OdaNo = ui.cmbodaNo.currentText()
    Kat = ui.cmbkat.currentText()

    try:
        ekle = "insert into urun (urunKodu,urunAdi,dolapNo,stokMiktari,urunAciklaması,odaNo,kat) values (?,?,?,?,?,?,?)"
        islem.execute(ekle,(UrunKodu,UrunAdi,DolapNo,StokMiktari,UrunAciklama,OdaNo,Kat))
        baglanti.commit()
        kayit_listele()
        ui.statusbar.showMessage("Kayıt Ekleme İşlemi Başarılı",10000)
        ui.lneurunAciklama.clear()
        ui.lneurunKodu.clear()
        ui.lneurunAdi.clear()
        ui.lnedolapNo.clear()
        ui.lnestokMiktari.clear()
        ui.cmbodaNo.setCurrentIndex(0)
        ui.cmbkat.setCurrentIndex(0)
    except Exception as error:
        ui.statusbar.showMessage("Kayıt Eklenemedi Hata Çıktı === "+str(error))



def kayit_listele():
    ui.tblListele.clear()
    ui.tblListele.setHorizontalHeaderLabels(("ID","Ürün Adı","Dolap No","Stok Miktarı","Ürün Açıklama","Oda No","Kat"))
    ui.tblListele.horizontalHeader().setSectionResizeMode(QHeaderView.Stretch)
    sorgu = "select * from urun"
    islem.execute(sorgu)
    ui.cmbKategorListele.setCurrentIndex(0)
    ui.cmbKategorListele1.setCurrentIndex(0)

    for indexSatir, kayitNumarasi in enumerate(islem):
        for indexSutun, kayitSutun in enumerate(kayitNumarasi):
            ui.tblListele.setItem(indexSatir,indexSutun,QTableWidgetItem(str(kayitSutun)))

kayit_listele()  
def kategoriye_gore_listele():
    listelenecek_kategori = ui.cmbKategorListele.currentText()
    listelenecek_kategori1 = ui.cmbKategorListele1.currentText()
    try:
        sorgu = "select * from urun where kat = ? and odaNo = ?"
        islem.execute(sorgu,(listelenecek_kategori,listelenecek_kategori1))
        ui.tblListele.clear()
        ui.tblListele.setHorizontalHeaderLabels(("ID", "Ürün Adı", "Dolap No", "Stok Miktarı", "Ürün Açıklama", "Oda No", "Kat"))
        ui.tblListele.horizontalHeader().setSectionResizeMode(QHeaderView.Stretch)
        for indexSatir, kayitNumarasi in enumerate(islem):
            for indexSutun, kayitSutun in enumerate(kayitNumarasi):
               ui.tblListele.setItem(indexSatir,indexSutun,QTableWidgetItem(str(kayitSutun)))
    except Exception as error:
        ui.statusbar.showMessage("Kayıt Eklenemedi Hata Çıktı === " + str(error))

def kayit_sil():
    sil_mesaj = QMessageBox.question(pencere,"Silme Onayı","Silmek İstediğinizden Emin Misiniz ?",QMessageBox.Yes | QMessageBox.No)

    if sil_mesaj == QMessageBox.Yes:
        secilen_kayit = ui.tblListele.selectedItems()
        silinecek_kayit = secilen_kayit[0].text()

        sorgu = "delete from urun where urunKodu = ?"
        try:
            islem.execute(sorgu,(silinecek_kayit,))
            baglanti.commit()
            ui.statusbar.showMessage("Kayıt Başarıyla Silindi")
            kayit_listele()
        except Exception as error:
            ui.statusbar.showMessage("Kayıt Silinirken Hata Çıktı === "+str(error))
    
    else:
        ui.statusbar.showMessage("Silme İşlemi İptal Edildi")
    

def kayit_guncelle():
    guncelle_mesaj = QMessageBox.question(pencere,"Güncelleme Onayı","Bu kaydı Güncellemek istediğinizden Emin Misiniz ?",QMessageBox.Yes | QMessageBox.No)

    if guncelle_mesaj == QMessageBox.Yes:
        try:
            UrunKodu = ui.lneurunKodu.text()
            UrunAdi = ui.lneurunAdi.text()
            DolapNo = ui.lnedolapNo.text()
            StokMiktari = ui.lnestokMiktari.text()
            UrunAciklama = ui.lneurunAciklama.text()
            OdaNo = ui.cmbodaNo.currentText()
            Kat = ui.cmbkat.currentText()

            if UrunAdi == "" and DolapNo == "" and StokMiktari == "" and UrunAciklama == "" and OdaNo == "":
                islem.execute("update urun set kat = ? where urunKodu = ?",(Kat,UrunKodu))
            elif UrunAdi == "" and DolapNo == "" and StokMiktari == "" and UrunAciklama == "" and Kat == "":
                islem.execute("update urun set odaNo = ? where urunKodu = ?",(OdaNo,UrunKodu))
            elif UrunAdi == "" and DolapNo == "" and StokMiktari == "" and OdaNo == "" and Kat == "":
                islem.execute("update urun set urunAciklaması = ? where urunKodu = ?",(UrunAciklama,UrunKodu))
            elif UrunAdi == "" and DolapNo == "" and UrunAciklama == "" and OdaNo == "" and Kat == "":
                islem.execute("update urun set stokMiktari = ? where urunKodu = ?",(StokMiktari,UrunKodu))
            elif UrunAdi == "" and StokMiktari == "" and UrunAciklama == "" and OdaNo == "" and Kat == "":
                islem.execute("update urun set dolapNo = ? where urunKodu = ?",(DolapNo,UrunKodu))
            elif DolapNo == "" and StokMiktari == "" and UrunAciklama == "" and OdaNo == "" and Kat == "":
                islem.execute("update urun set urunAdi = ? where urunKodu = ?",(UrunAdi,UrunKodu))
            else:
                islem.execute("update urun set urunAdi = ?, dolapNo = ? , stokMiktari = ?, urunAciklaması = ?, odaNo = ?, kat = ? where urunKodu = ?",(UrunAdi,DolapNo,StokMiktari,UrunAciklama,OdaNo, Kat,UrunKodu))
            baglanti.commit()
            kayit_listele()
            ui.statusbar.showMessage("Kayıt Başarıyla Güncellendi")
            ui.lneurunAciklama.clear()
            ui.lneurunKodu.clear()
            ui.lneurunAdi.clear()
            ui.lnedolapNo.clear()
            ui.lnestokMiktari.clear()
            ui.cmbodaNo.setCurrentIndex(0)
            ui.cmbkat.setCurrentIndex(0)
        except Exception as error:
            ui.statusbar.showMessage("Kayıt Güncellemede Hata Çıktı === "+str(error))
    else:
        ui.statusbar.showMessage("Güncelleme İptal Edildi")
 
def cikisYap():
    cikis_mesaj = QMessageBox.question(pencere,"Oturumu Kapatın","Çıkış Yapmak İstediğinize Emin Misiniz ?",QMessageBox.Yes | QMessageBox.No)

    if cikis_mesaj == QMessageBox.Yes:
        uygulama.closeAllWindows()
        call(["python", "giris.py"])

    else:
        ui.statusbar.showMessage("Çıkış İptal Edildi")

ui.btnEkle.clicked.connect(kayit_ekle)
ui.btnListele.clicked.connect(kayit_listele)
ui.btnKategoriyeGoreListele.clicked.connect(kategoriye_gore_listele)
ui.btnSil.clicked.connect(kayit_sil)
ui.btnGuncelle.clicked.connect(kayit_guncelle)
ui.cikisYap.clicked.connect(cikisYap)
sys.exit(uygulama.exec_())
