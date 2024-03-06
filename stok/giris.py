from tkinter import *
from tkinter import messagebox
import time
import sqlite3
from PIL import ImageTk, Image 
from subprocess import call


class Uygulama(object):
    def __init__(self):
        self.sqlite = None  
        self.mrt = None  
        self.kayitli_kullanici = None  
        self.max_kullanici = 30  
        self.ekran = None
        self.ekran_baslik = "STOK TAKİP GİRİŞ VE KAYIT"  
        self.ekran_resulation = "325x400+100+100"  
        self.giris_butonu = None  
        self.kayit_butonu = None 
        self.cikis_butonu = None  
        self.ooc = False  
        self.giris_ekran = None 
        self.kayit_ekran = None 
        self.kullanici_gir = None 
        self.sifre_gir = None  
        self.kullanici_input = None  
        self.sifre_input = None
        self.cerceve_destroy = None 
        self.cerceve_cikis = None 
        self.cerceve_onayla = None  
        self.giris_bilgisi = None 
        self.kayit_bilgisi = None 
        self.database()
        self.main()

    def database(self):
        self.sqlite = sqlite3.connect("database/db_1.db")
        self.mrt = self.sqlite.cursor()
        self.mrt.execute("CREATE TABLE IF NOT EXISTS KULLANICI_BILGILERI (KULLANICI_ADI TEXT, SIFRE TEXT)")
        self.sqlite.commit()
        self.sqlite.close()
        

    def main(self):
        self.ekran = Tk()
        self.ekran.geometry(self.ekran_resulation)
        self.ekran.title(self.ekran_baslik)
        self.ekran.resizable(FALSE, FALSE)
        self.bg = PhotoImage(file = "bg_1.png" )
        self.label1 = Label(self.ekran, image = self.bg)
        self.label1.place(x = 0, y = 0) 
        self.giris_butonu = Button(self.ekran, text="GİRİŞ YAP", width=10, command=self.giris_ekrani)
        self.giris_butonu.pack(side=TOP, pady=25)
        self.kayit_butonu = Button(self.ekran, text="KAYIT OL", width=10, command=self.kayit_ekrani)
        self.kayit_butonu.pack(side=TOP)
        self.cikis_butonu = Button(self.ekran, text="ÇIKIŞ YAP", width=10, command=self.cikis)
        self.cikis_butonu.pack(side=TOP, pady=25)
        self.ooc = False
        mainloop()

    def giris_ekrani(self):
        if not self.ooc:
            self.giris_cercevesi = Frame(self.ekran, bd=5, relief=RAISED)
            self.giris_cercevesi.pack(pady=10)
            self.giris_bilgisi = Label(self.giris_cercevesi, text="GİRİŞ EKRANI", width=42)
            self.giris_bilgisi.grid(row=1, column=1, columnspan=2)
            self.kullanici_gir = Label(self.giris_cercevesi, text="KULLANICI ADI >>>", width=20)
            self.kullanici_gir.grid(row=2, column=1)
            self.sifre_gir = Label(self.giris_cercevesi, text="ŞİFRE >>>", width=20)
            self.sifre_gir.grid(row=3, column=1)
            self.kullanici_input = Entry(self.giris_cercevesi, width=20)
            self.kullanici_input.grid(row=2, column=2)
            self.sifre_input = Entry(self.giris_cercevesi, width=20, show="*")
            self.sifre_input.grid(row=3, column=2)
            self.cerceve_onay = Button(self.giris_cercevesi, text="GİRİŞ YAP", width=20, bd=2, relief=RAISED,
                                       command=self.giris_onay)
            self.cerceve_onay.grid(row=4, column=1, pady=10)
            self.cerceve_destroy = Button(self.giris_cercevesi, text="SİL", width=20, bd=2, relief=RAISED,
                                            command=self.cerceve_destroyall)
            self.cerceve_destroy.grid(row=4, column=2)
            self.cerceve_cikis = Button(self.giris_cercevesi, text="GERİ DÖN", width=42, bd=2, relief=RAISED,
                                        command=self.cikis1)
            self.cerceve_cikis.grid(row=5, column=1, columnspan=2, pady=10)
            self.ooc = True

    def kayit_ekrani(self):

        if not self.ooc:
            self.kayit_cercevesi = Frame(self.ekran, bd=5, relief=RAISED)
            self.kayit_cercevesi.pack(pady=10)
            self.kayit_bilgisi = Label(self.kayit_cercevesi, text="KAYIT EKRANI", width=42)
            self.kayit_bilgisi.grid(row=1, column=1, columnspan=2)
            self.kullanici_gir = Label(self.kayit_cercevesi, text="KULLANICI ADI >>>", width=20)
            self.kullanici_gir.grid(row=2, column=1)
            self.sifre_gir = Label(self.kayit_cercevesi, text="ŞİFRE >>>", width=20)
            self.sifre_gir.grid(row=3, column=1)
            self.kullanici_input = Entry(self.kayit_cercevesi, width=20)
            self.kullanici_input.grid(row=2, column=2)
            self.sifre_input = Entry(self.kayit_cercevesi, width=20, show="*")
            self.sifre_input.grid(row=3, column=2)
            self.cerceve_onay = Button(self.kayit_cercevesi, text="KAYIT OL", width=20, bd=2, relief=RAISED,
                                       command=self.kayit_onay)
            self.cerceve_onay.grid(row=4, column=1, pady=10)
            self.cerceve_destroy = Button(self.kayit_cercevesi, text="SİL", width=20, bd=2, relief=RAISED,
                                            command=self.cerceve_destroyall)
            self.cerceve_destroy.grid(row=4, column=2)
            self.cerceve_cikis = Button(self.kayit_cercevesi, text="GERİ DÖN", width=42, bd=2, relief=RAISED,
                                        command=self.cikis2)
            self.cerceve_cikis.grid(row=5, column=1, columnspan=2, pady=10)
            self.ooc = True

    def cikis(self):
        if not self.ooc:
            self.ekran.quit()
            self.ooc = False

    def cikis1(self):
        if self.ooc:
            self.giris_cercevesi.destroy()
            self.ooc = False

    def cikis2(self):
        if self.ooc:
            self.kayit_cercevesi.destroy()
            self.ooc = False

    def cerceve_destroyall(self):
        self.kullanici_input.delete(0, END)
        self.sifre_input.delete(0, END)

    def giris_onay(self):
        if 4 < len(self.sifre_input.get()) < 16:
            self.sqlite = sqlite3.connect("database/db_1.db")
            self.mrt = self.sqlite.cursor()
            self.mrt.execute("SELECT * FROM KULLANICI_BILGILERI WHERE KULLANICI_ADI = ? AND SIFRE = ?",
                               (self.kullanici_input.get(), self.sifre_input.get()))
            self.kayitli_kullanici = self.mrt.fetchall()
            if len(self.kayitli_kullanici) == 0:
                messagebox.showerror("HATA", "KULLANICI ADI VEYA ŞİFRE YANLIŞ!")
            else:
                self.sqlite.close()
                messagebox.showinfo("BAŞARILI", "KULLANICI ADI VE ŞİFRESİ DOĞRU")
                self.kullanici_input.delete(0, END)
                self.sifre_input.delete(0, END)
                self.cikis1()
                self.giris_bilgisi = Label(self.ekran, text="STOK TAKİP PROGRAMINA HOŞGELDİNİZ")
                self.giris_bilgisi.pack()
                time.sleep(1)
                self.ekran.destroy()
                call(["python", "main.py"])
        else:
            messagebox.showerror("HATA", "ŞİFRENİZ EN AZ 5 EN ÇOK 15 KARAKTERDEN OLUŞMALIDIR.")
            self.kullanici_input.delete(0, END)
            self.sifre_input.delete(0, END)

    def kayit_onay(self):
        if 4 < len(self.sifre_input.get()) < 16:
            self.sqlite = sqlite3.connect("database/db_1.db")
            self.mrt = self.sqlite.cursor()
            self.mrt.execute("SELECT KULLANICI_ADI, SIFRE FROM KULLANICI_BILGILERI")
            self.kayitli_kullanici = self.mrt.fetchall()
            if len(self.kayitli_kullanici) == self.max_kullanici:
                messagebox.showerror("HATA", "KULLANICI ZATEN MEVCUT! LÜTFEN GİRİŞ YAPINIZ!")
                self.sqlite.close()
                self.kullanici_input.delete(0, END)
                self.sifre_input.delete(0, END)
            else:  
                self.mrt.execute('SELECT * FROM KULLANICI_BILGILERI WHERE KULLANICI_ADI = ?',
                                   (self.kullanici_input.get(),))
                self.kayitli_kullanici = self.mrt.fetchall()
                if self.kayitli_kullanici:
                    messagebox.showerror("HATA", "KULLANICI ZATEN MEVCUT! LÜTFEN GİRİŞ YAPINIZ")
                    self.kullanici_input.delete(0, END)
                    self.sifre_input.delete(0, END)
                else:
                    self.mrt.execute("INSERT INTO KULLANICI_BILGILERI VALUES (?,?)", (self.kullanici_input.get(),
                                                                                  self.sifre_input.get()))
                    self.sqlite.commit()
                    self.sqlite.close()
                    messagebox.showinfo("KAYIT BASARILI", "KAYIT BAŞARIYLA TAMAMLANDI")
                    self.kullanici_input.delete(0, END)
                    self.sifre_input.delete(0, END)
                    self.kayit_cercevesi.destroy()
                    self.ooc = False
        else:
            messagebox.showerror("HATA", "ŞİFRENİZ EN AZ 5 EN ÇOK 15 KARAKTERDEN OLUŞMALIDIR.")
            self.kullanici_input.delete(0, END)
            self.sifre_input.delete(0, END)


app = Uygulama()